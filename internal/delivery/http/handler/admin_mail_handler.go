package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"
	"campusassistant-api/pkg/mailer"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// maxBroadcastRecipients is the hard cap per call. The SMTP account (Gmail
// free tier) allows roughly 500 messages/day, and the same account serves the
// password-reset flow — a fat-fingered broadcast of thousands must not be
// able to starve it. Admins who need more send in batches.
const maxBroadcastRecipients = 50

// AdminMailHandler sends email to students on behalf of the admin dashboard:
// the per-student claim-code mail lives in StudentHandler; this one is the
// free-form broadcast. AdminJWT-gated in router.go — never wire it lower.
type AdminMailHandler struct {
	db   *gorm.DB
	mail mailer.Mailer
}

func NewAdminMailHandler(db *gorm.DB, mail mailer.Mailer) *AdminMailHandler {
	return &AdminMailHandler{db: db, mail: mail}
}

// Send broadcasts a subject+message to an explicit list of student profiles.
// POST /admin/mail (AdminJWT).
//
//	{student_ids: ["uuid", ...], subject: "...", message: "..."} ->
//	200 {sent, skipped: [{student_id, reason}]}
//
// Recipients must be explicit student IDs: resolve them server-side so the
// dashboard can never send to an address that isn't attached to a real
// student profile. Profiles without a usable email, already-claimed profiles
// (their code flow is done; treat as opt-in-boundary) and unparseable
// addresses are skipped, not failed — a partially valid batch still goes out.
func (h *AdminMailHandler) Send(c *gin.Context) {
	var req struct {
		StudentIDs []uuid.UUID `json:"student_ids" binding:"required,min=1"`
		Subject    string      `json:"subject" binding:"required,max=200"`
		Message    string      `json:"message" binding:"required,max=20000"`
		SenderName string      `json:"sender_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A subject, message and at least one student_id are required" + ": " + err.Error()})
		return
	}
	if len(req.StudentIDs) > maxBroadcastRecipients {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Too many recipients — send in batches",
			"max":   maxBroadcastRecipients,
		})
		return
	}

	// De-duplicate: it's cheap here and prevents double-mailing when the
	// dashboard passes overlapping selections.
	seen := make(map[uuid.UUID]struct{}, len(req.StudentIDs))
	ids := make([]uuid.UUID, 0, len(req.StudentIDs))
	for _, id := range req.StudentIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	var students []domain.Student
	if err := h.db.Where("id IN ?", ids).Find(&students).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to look up students"})
		return
	}

	found := make(map[uuid.UUID]domain.Student, len(students))
	for _, s := range students {
		found[s.ID] = s
	}

	type skipped struct {
		StudentID uuid.UUID `json:"student_id"`
		Reason    string    `json:"reason"`
	}

	senderName := strings.TrimSpace(req.SenderName)
	if senderName == "" {
		senderName = "Campus Assistant"
	}

	htmlBody, textBody := mailer.BroadcastBodies(req.Subject, req.Message, senderName)

	var (
		sent  int
		skips []skipped
	)

	for _, st := range students {
		email := strings.TrimSpace(st.Email)
		if _, err := mail.ParseAddress(email); err != nil {
			if email == "" {
				skips = append(skips, skipped{st.ID, "no email on file"})
			} else {
				skips = append(skips, skipped{st.ID, "invalid email address"})
			}
			continue
		}
		if err := h.mail.Send(email, req.Subject, htmlBody, textBody); err != nil {
			logger.Errorf("Admin mail: failed to email %s: %v", email, err)
			skips = append(skips, skipped{st.ID, "send failed"})
			continue
		}
		sent++
	}

	// IDs the DB has no row for — reported per-request, unlike the per-
	// recipient skips above.
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			skips = append(skips, skipped{id, "student not found"})
		}
	}

	logger.Infof("Admin mail: sent %d/%d for subject %q", sent, len(ids), req.Subject)

	c.JSON(http.StatusOK, gin.H{
		"sent":    sent,
		"skipped": skips,
	})
}
