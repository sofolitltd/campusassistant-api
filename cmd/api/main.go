package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"campusassistant-api/internal/config"
	httpDelivery "campusassistant-api/internal/delivery/http"
	"campusassistant-api/internal/service"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/pkg/bkash"
	"campusassistant-api/pkg/logger"
)

func main() {
	// 1. Load Configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. Initialize Logger
	logger.InitLogger(cfg.Environment)
	logger.Infof("Starting Campus Assistant API in %s mode", cfg.Environment)

	// Refuse to start unauthenticated. APIKeyMiddleware used to wave every
	// request through when this was empty, so a dropped env var in production
	// silently exposed the whole API rather than failing loudly.
	if cfg.APIKeyRequired && cfg.APIKey == "" && cfg.Environment != "development" {
		logger.Fatalf("API_KEY is required outside development (or set API_KEY_REQUIRED=false with ACCESS_ENFORCE=true); refusing to start unauthenticated")
	}
	// The API key is the only thing standing between the open internet and
	// any route the access policy hasn't been enforced on yet. Dropping it
	// is only safe once those policies actually block.
	if !cfg.APIKeyRequired && !cfg.AccessEnforce && cfg.Environment != "development" {
		logger.Fatalf("API_KEY_REQUIRED=false requires ACCESS_ENFORCE=true: without the key and without enforced route policies, routes would be open")
	}
	if cfg.Environment != "development" && cfg.CORSAllowedOrigins == "" {
		logger.Warnf("CORS_ALLOWED_ORIGINS is empty: only the BKASH_CALLBACK_BASE_URL origin may call the API from a browser")
	}

	// 3. Database Connection
	db, err := postgres.NewConnection(cfg)
	if err != nil {
		logger.Fatalf("Database connection failed: %v", err)
	}

	// 4. Run Migrations (Conditional)
	// Only run migrations in development or if explicitly enabled
	if cfg.Environment == "development" || cfg.DBAutoMigrate {
		logger.Infof("Migrations enabled (Mode: %s, AutoMigrate: %v). Running...", cfg.Environment, cfg.DBAutoMigrate)
		if err := postgres.RunMigrations(db); err != nil {
			logger.Fatalf("Migration failed: %v", err)
		}
	} else {
		logger.Infof("Migrations skipped for production (Set DB_AUTO_MIGRATE=true to enable)")
	}

	// 5. Setup Router
	r := httpDelivery.NewRouter(cfg, db)

	// 5. Background Workers
	subRepo := postgres.NewSubscriptionRepository(db)
	chatRepo := postgres.NewChatRepository(db)
	bkashTxRepo := postgres.NewBkashTransactionRepository(db)
	couponRepo := postgres.NewCouponRepository(db)
	// Payment services for the reconciliation worker. (The router builds its
	// own stateless instances for request handling.) The worker needs the real
	// bKash client — it has to ask bKash about pending payments.
	bkashClient := bkash.NewClient(cfg)
	billingSvc := service.NewBillingService(db)
	paymentSvc := service.NewPaymentService(db, subRepo, bkashTxRepo, couponRepo, bkashClient, cfg.BkashCallbackBaseURL, billingSvc)
	marketplacePaymentSvc := service.NewMarketplacePaymentService(db, postgres.NewOrderRepository(db), postgres.NewOrderTransactionRepository(db), bkashClient, cfg.BkashCallbackBaseURL, billingSvc)
	orderSvc := service.NewOrderService(db, billingSvc, nil) // no notifier: abandoned checkouts don't need a "cancelled" ping
	go runPaymentReconciler(paymentSvc, marketplacePaymentSvc, billingSvc, orderSvc)
	go func() {
		subTicker := time.NewTicker(1 * time.Hour)
		cleanupTicker := time.NewTicker(24 * time.Hour)
		defer subTicker.Stop()
		defer cleanupTicker.Stop()
		for {
			select {
			case <-subTicker.C:
				count, err := subRepo.ExpireSubscriptions(context.Background())
				if err != nil {
					log.Printf("Background Worker Error: Failed to expire subscriptions: %v", err)
				} else if count > 0 {
					log.Printf("Background Worker: Expired %d user subscriptions automatically.", count)
				}

			case <-cleanupTicker.C:
				cutoff := time.Now().Add(-90 * 24 * time.Hour) // 90 days
				count, err := chatRepo.CleanupDeletedMessages(context.Background(), cutoff)
				if err != nil {
					log.Printf("Background Worker Error: Failed to cleanup deleted messages: %v", err)
				} else if count > 0 {
					log.Printf("Background Worker: Cleaned up %d deleted message records.", count)
				}

				// Password reset rows are short-lived (10 min code, 15 min
				// token). Anything a day old is spent — hard-delete it rather
				// than accumulating consumed code hashes forever.
				resetCutoff := time.Now().Add(-24 * time.Hour)
				res := db.Unscoped().
					Where("created_at < ?", resetCutoff).
					Delete(&domain.PasswordReset{})
				if res.Error != nil {
					log.Printf("Background Worker Error: Failed to cleanup password resets: %v", res.Error)
				} else if res.RowsAffected > 0 {
					log.Printf("Background Worker: Cleaned up %d expired password reset records.", res.RowsAffected)
				}

				// Notifications had no retention policy at all: every admin
				// broadcast fans out into one NotificationRecipient row per
				// user (see NotificationService.SendToUsers), so the table
				// grows as notifications*users forever with nothing trimming
				// it. Old delivery/read-state is worthless past 90 days —
				// hard-delete recipients first, then any content rows left
				// with no recipients (scope=user sends to a since-deleted
				// user, or a broadcast whose audience was empty).
				notifCutoff := time.Now().Add(-90 * 24 * time.Hour)
				recipRes := db.Unscoped().
					Where("created_at < ?", notifCutoff).
					Delete(&domain.NotificationRecipient{})
				if recipRes.Error != nil {
					log.Printf("Background Worker Error: Failed to cleanup notification recipients: %v", recipRes.Error)
				} else if recipRes.RowsAffected > 0 {
					log.Printf("Background Worker: Cleaned up %d expired notification recipient records.", recipRes.RowsAffected)
				}

				notifRes := db.Unscoped().
					Where("created_at < ? AND id NOT IN (SELECT DISTINCT notification_id FROM notification_recipients)", notifCutoff).
					Delete(&domain.Notification{})
				if notifRes.Error != nil {
					log.Printf("Background Worker Error: Failed to cleanup notifications: %v", notifRes.Error)
				} else if notifRes.RowsAffected > 0 {
					log.Printf("Background Worker: Cleaned up %d expired notification records.", notifRes.RowsAffected)
				}
			}
		}
	}()

	// 6. Start Server
	serverAddr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Server listening on %s", serverAddr)
	if err := r.Run(serverAddr); err != nil {
		logger.Fatalf("Server failed to start: %v", err)
	}
}

// runPaymentReconciler closes the gap left by client-driven payment
// completion. Every few minutes it asks bKash about payments that are still
// "initiated" — recovering ones the user paid but whose app never called
// execute, and closing abandoned checkouts (releasing their coupon uses) —
// and books merchant earnings for delivered orders whose follow-up failed.
//
// Every step is idempotent and row-locked, so running this on several
// instances at once is safe (they just do redundant bKash queries).
func runPaymentReconciler(subs *service.PaymentService, orders *service.MarketplacePaymentService, billing *service.BillingService, orderSvc *service.OrderService) {
	const (
		interval    = 5 * time.Minute
		minAge      = 2 * time.Minute // give an in-flight checkout time to finish normally
		cancelAfter = 1 * time.Hour   // an unpaid session this old is abandoned
		batch       = 100
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)

		if st, err := subs.ReconcilePending(ctx, minAge, cancelAfter, batch); err != nil {
			logger.Errorf("[reconcile] subscription payments: %v", err)
		} else if st.Recovered+st.Cancelled+st.Errors > 0 {
			logger.Infof("[reconcile] subscription payments: checked=%d recovered=%d cancelled=%d errors=%d", st.Checked, st.Recovered, st.Cancelled, st.Errors)
		}

		if st, err := orders.ReconcilePending(ctx, minAge, cancelAfter, batch); err != nil {
			logger.Errorf("[reconcile] order payments: %v", err)
		} else if st.Recovered+st.Cancelled+st.Errors > 0 {
			logger.Infof("[reconcile] order payments: checked=%d recovered=%d cancelled=%d errors=%d", st.Checked, st.Recovered, st.Cancelled, st.Errors)
		}

		// An unpaid order keeps its stock reserved; give it back once the
		// payment window has clearly passed.
		if n, err := orderSvc.ExpireUnpaid(ctx, 2*time.Hour, batch); err != nil {
			logger.Errorf("[orders] expire unpaid orders: %v", err)
		} else if n > 0 {
			logger.Infof("[orders] cancelled %d unpaid orders and returned their stock", n)
		}

		if n, err := billing.ReleaseDeliveredOrders(ctx, batch); err != nil {
			logger.Errorf("[billing] release delivered orders: %v", err)
		} else if n > 0 {
			logger.Infof("[billing] booked earnings for %d delivered orders", n)
		}
		cancel()
	}
}
