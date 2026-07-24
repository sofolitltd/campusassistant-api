package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CheckoutRequest struct {
	AddressID     uuid.UUID             `json:"address_id" binding:"required"`
	Items         []CheckoutItemRequest `json:"items" binding:"required"`
	PaymentMethod domain.PaymentMethod  `json:"payment_method"`
}

type CheckoutItemRequest struct {
	ProductID uuid.UUID `json:"product_id" binding:"required"`
	Quantity  int       `json:"quantity" binding:"required,min=1"`
}

type CreateMarketplacePaymentRequest struct {
	OrderID uuid.UUID `json:"order_id" binding:"required"`
}

type ExecuteMarketplacePaymentRequest struct {
	PaymentID string `json:"payment_id" binding:"required"`
}

type UpdateOrderStatusRequest struct {
	Status domain.OrderStatus `json:"status" binding:"required"`
}

// OrderHandler serves admin order management and buyer self-service.
type OrderHandler struct {
	orderRepo    domain.OrderRepository
	productRepo  domain.ProductRepository
	merchantRepo domain.MerchantRepository
	addressRepo  domain.AddressRepository
	paymentSvc   *service.MarketplacePaymentService
}

func NewOrderHandler(
	orderRepo domain.OrderRepository,
	productRepo domain.ProductRepository,
	merchantRepo domain.MerchantRepository,
	addressRepo domain.AddressRepository,
	paymentSvc *service.MarketplacePaymentService,
) *OrderHandler {
	return &OrderHandler{
		orderRepo:    orderRepo,
		productRepo:  productRepo,
		merchantRepo: merchantRepo,
		addressRepo:  addressRepo,
		paymentSvc:   paymentSvc,
	}
}

// ==============================
// Admin endpoints (API-key gated)
// ==============================

// GetAllOrders lists all orders, optionally filtered by ?status=.
func (h *OrderHandler) GetAllOrders(c *gin.Context) {
	status := domain.OrderStatus(c.Query("status"))
	orders, err := h.orderRepo.GetAll(c.Request.Context(), status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch orders"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": orders})
}

// GetOrderByID returns a single order by ID (admin).
func (h *OrderHandler) GetOrderByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order id"})
		return
	}
	order, err := h.orderRepo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	c.JSON(http.StatusOK, order)
}

// UpdateOrderStatus progresses an order through its fulfillment lifecycle
// (paid->processing->shipped->delivered, or cancelled at any point).
func (h *OrderHandler) UpdateOrderStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order id"})
		return
	}
	var req UpdateOrderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.orderRepo.UpdateStatus(c.Request.Context(), id, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update order status"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Order status updated"})
}

// ==============================
// Buyer endpoints (JWT gated)
// ==============================

// Checkout creates an Order + OrderItems from a cart payload + address_id.
func (h *OrderHandler) Checkout(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var req CheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Load the address to snapshot its fields
	address, err := h.addressRepo.GetByID(c.Request.Context(), req.AddressID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Address not found"})
		return
	}
	if address.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this address"})
		return
	}

	var totalAmount int
	var items []domain.OrderItem

	for _, ci := range req.Items {
		product, err := h.productRepo.GetProductByID(c.Request.Context(), ci.ProductID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Product not found: " + ci.ProductID.String()})
			return
		}

		// Resolve commission rate from the product's merchant
		commissionRate := 0.0
		if product.MerchantID != uuid.Nil {
			merchant, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), product.MerchantID)
			if err == nil {
				commissionRate = merchant.CommissionRate
			}
		}

		itemTotal := product.Price * ci.Quantity
		totalAmount += itemTotal

		items = append(items, domain.OrderItem{
			ProductID:              product.ID,
			ProductTitle:           product.Title,
			MerchantID:             product.MerchantID,
			Quantity:               ci.Quantity,
			UnitPrice:              product.Price,
			CommissionRateSnapshot: commissionRate,
		})
	}

	paymentMethod := req.PaymentMethod
	if paymentMethod != domain.PaymentMethodCashOnDelivery {
		paymentMethod = domain.PaymentMethodBkash
	}

	// Cash on delivery skips the payment gateway entirely — the order goes
	// straight to processing and gets marked paid on delivery.
	status := domain.OrderStatusPendingPayment
	if paymentMethod == domain.PaymentMethodCashOnDelivery {
		status = domain.OrderStatusProcessing
	}

	order := &domain.Order{
		BuyerID:               userID,
		ShippingRecipientName: address.RecipientName,
		ShippingPhone:         address.Phone,
		ShippingAddressLine:   address.AddressLine,
		ShippingCity:          address.City,
		TotalAmount:           totalAmount,
		Status:                status,
		PaymentMethod:         paymentMethod,
		Items:                 items,
	}

	if err := h.orderRepo.Create(c.Request.Context(), order); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create order"})
		return
	}

	c.JSON(http.StatusOK, order)
}

// ListMyOrders returns the current user's own orders.
func (h *OrderHandler) ListMyOrders(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	orders, err := h.orderRepo.GetByBuyer(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch orders"})
		return
	}
	c.JSON(http.StatusOK, orders)
}

// GetMyOrder returns a single order, ownership-checked.
func (h *OrderHandler) GetMyOrder(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order id"})
		return
	}
	order, err := h.orderRepo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	if order.BuyerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this order"})
		return
	}
	c.JSON(http.StatusOK, order)
}

// ListMerchantOrders returns every order containing at least one item sold
// by the given merchant (ownership-checked against the JWT user), with
// Items trimmed down to only that merchant's own line items so a merchant
// never sees another seller's items from the same checkout.
func (h *OrderHandler) ListMerchantOrders(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	merchant, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), merchantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return
	}
	if merchant.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return
	}

	orders, err := h.orderRepo.GetByMerchant(c.Request.Context(), merchantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch orders"})
		return
	}
	for i := range orders {
		filtered := orders[i].Items[:0]
		for _, item := range orders[i].Items {
			if item.MerchantID == merchantID {
				filtered = append(filtered, item)
			}
		}
		orders[i].Items = filtered
	}
	c.JSON(http.StatusOK, gin.H{"data": orders})
}

// merchantAllowedStatuses are the only transitions a merchant may make
// themselves — payment/cancellation states stay admin-controlled since
// those tie into the payment gateway and refund flow, not fulfillment.
var merchantAllowedStatuses = map[domain.OrderStatus]bool{
	domain.OrderStatusShipped:   true,
	domain.OrderStatusDelivered: true,
}

// MerchantUpdateOrderStatus lets a merchant progress fulfillment (mark an
// order shipped/delivered) on an order containing their own items —
// ownership-checked against both the merchant and the order itself.
func (h *OrderHandler) MerchantUpdateOrderStatus(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order id"})
		return
	}

	merchant, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), merchantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return
	}
	if merchant.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return
	}

	var req UpdateOrderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !merchantAllowedStatuses[req.Status] {
		c.JSON(http.StatusForbidden, gin.H{"error": "Merchants may only set status to shipped or delivered"})
		return
	}

	order, err := h.orderRepo.GetByID(c.Request.Context(), orderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	hasItem := false
	for _, item := range order.Items {
		if item.MerchantID == merchantID {
			hasItem = true
			break
		}
	}
	if !hasItem {
		c.JSON(http.StatusForbidden, gin.H{"error": "This order has no items from your business"})
		return
	}

	if err := h.orderRepo.UpdateStatus(c.Request.Context(), orderID, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update order status"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Order status updated"})
}

// CreateMarketplacePayment starts a bKash checkout for an order.
func (h *OrderHandler) CreateMarketplacePayment(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var req CreateMarketplacePaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.paymentSvc.CreatePayment(c.Request.Context(), userID, req.OrderID)
	if err != nil {
		switch err {
		case service.ErrOrderNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		case service.ErrForbidden:
			c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this order"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

// ExecuteMarketplacePayment finalizes a bKash payment and marks the order paid.
func (h *OrderHandler) ExecuteMarketplacePayment(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var req ExecuteMarketplacePaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.paymentSvc.ExecutePayment(c.Request.Context(), userID, req.PaymentID); err != nil {
		switch err {
		case service.ErrOrderTxNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "Payment transaction not found"})
		case service.ErrForbidden:
			c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this payment"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Payment executed successfully"})
}
