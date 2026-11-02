package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"reflect"
	"strings"
	"time"

	"campusassistant-api/internal/delivery/http/middleware"
	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Event struct {
	ActorType    string
	ActorID      uuid.UUID
	ActorLabel   string
	ViaRole      string
	Action       string
	EntityType   string
	EntityID     uuid.UUID
	UniversityID *uuid.UUID
	DepartmentID *uuid.UUID
	BatchID      *uuid.UUID
	Changes      any
	Reason       string
	IPAddress    string
	UserAgent    string
}

func FromRequest(c *gin.Context, event Event) Event {
	if id, ok := c.Get("user_id"); ok {
		if uid, ok := id.(uuid.UUID); ok {
			event.ActorID = uid
		}
	}
	role, _ := c.Get("user_role")
	roleName, _ := role.(string)
	if middleware.IsAdminRole(roleName) {
		event.ActorType = "admin"
		if event.ViaRole == "" {
			event.ViaRole = "admin"
		}
	} else {
		event.ActorType = "user"
		if event.ViaRole == "" {
			event.ViaRole = "user"
		}
	}
	if label, ok := c.Get("user_email"); ok {
		event.ActorLabel, _ = label.(string)
	}
	event.IPAddress = net.ParseIP(c.ClientIP()).String()
	if event.IPAddress == "<nil>" {
		event.IPAddress = ""
	}
	event.UserAgent = c.Request.UserAgent()
	return event
}

func RecordRequest(c *gin.Context, db *gorm.DB, event Event) {
	event = FromRequest(c, event)
	if err := Record(db.WithContext(c.Request.Context()), event); err != nil {
		log.Printf("[audit] failed to write %s for %s %s: %v", event.Action, event.EntityType, event.EntityID, err)
		systemLog := domain.SystemLog{
			Method:    c.Request.Method,
			Path:      c.Request.URL.Path,
			Message:   "Audit write failed for " + event.Action + ": " + err.Error(),
			IPAddress: c.ClientIP(),
		}
		if event.ActorID != uuid.Nil {
			systemLog.UserID = &event.ActorID
		}
		if logErr := db.WithContext(c.Request.Context()).Create(&systemLog).Error; logErr != nil {
			log.Printf("[audit] failed to record audit failure in system_logs: %v", logErr)
		}
	}
}

func Record(db *gorm.DB, event Event) error {
	raw, err := json.Marshal(event.Changes)
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("decode audit changes: %w", err)
	}
	changes, err := json.Marshal(redact(decoded))
	if err != nil {
		return fmt.Errorf("redact audit changes: %w", err)
	}
	logEntry := domain.AuditLog{
		ActorType:    event.ActorType,
		ActorID:      event.ActorID,
		ActorLabel:   event.ActorLabel,
		ViaRole:      event.ViaRole,
		Action:       event.Action,
		EntityType:   event.EntityType,
		EntityID:     event.EntityID,
		UniversityID: event.UniversityID,
		DepartmentID: event.DepartmentID,
		BatchID:      event.BatchID,
		Changes:      datatypes.JSON(changes),
		Reason:       event.Reason,
		IPAddress:    event.IPAddress,
		UserAgent:    event.UserAgent,
		Timestamp:    time.Now(),
		UserID:       event.ActorID,
		EntityName:   event.EntityType,
		Description:  event.Action,
	}
	if err := db.Create(&logEntry).Error; err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func Diff(before, after any) map[string][2]any {
	oldFields := fields(before)
	newFields := fields(after)
	changes := make(map[string][2]any)
	for key, afterValue := range newFields {
		beforeValue, exists := oldFields[key]
		if !exists || !reflect.DeepEqual(beforeValue, afterValue) {
			changes[key] = [2]any{beforeValue, afterValue}
		}
	}
	return changes
}

func fields(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") ||
				strings.Contains(lower, "token") ||
				strings.Contains(lower, "verification_code") ||
				strings.Contains(lower, "document_url") ||
				strings.Contains(lower, "id_card") ||
				strings.Contains(lower, "second_doc") ||
				strings.HasSuffix(lower, "_url") {
				result[key] = "[redacted]"
			} else {
				result[key] = redact(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = redact(item)
		}
		return result
	default:
		return value
	}
}

func EntityName(entity any) string {
	typeOf := reflect.TypeOf(entity)
	if typeOf == nil {
		return "unknown"
	}
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	return strings.ToLower(typeOf.Name())
}
