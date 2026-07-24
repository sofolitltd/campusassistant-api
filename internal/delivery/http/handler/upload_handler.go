package handler

import (
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"
	"campusassistant-api/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// sensitiveUploadFolders are forced to Visibility="private" regardless of
// what the client requests — never trust client-supplied visibility for
// identity documents. Extend this set for future sensitive-document needs
// (payment proofs, dispute evidence, etc.) rather than adding another
// one-off upload path.
var sensitiveUploadFolders = map[string]bool{
	"merchant-verification": true,
}

// presignedURLTTL is how long a resolved private-attachment URL stays valid.
const presignedURLTTL = 10 * time.Minute

type UploadHandler struct {
	db      *gorm.DB
	storage *storage.R2Storage
}

func NewUploadHandler(db *gorm.DB, storage *storage.R2Storage) *UploadHandler {
	return &UploadHandler{
		db:      db,
		storage: storage,
	}
}

func (h *UploadHandler) UploadImage(c *gin.Context) {
	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No image uploaded"})
		return
	}

	// Optional: Add folder path if provided (e.g. "banners", "universities")
	folder := c.PostForm("folder")
	if folder == "" {
		folder = "uploads"
	}

	// Optional: Add reference_id if provided
	refIDStr := c.PostForm("reference_id")
	var refID uuid.UUID
	if refIDStr != "" {
		refID, _ = uuid.Parse(refIDStr)
	}

	// Generate unique path: {folder}/2026/02/uuid_filename.ext
	now := time.Now()
	uniqueID := uuid.New().String()
	ext := filepath.Ext(file.Filename)
	path := fmt.Sprintf("%s/%d/%02d/%s%s", folder, now.Year(), now.Month(), uniqueID, ext)

	fileURL, err := h.storage.UploadFile(c.Request.Context(), file, path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upload: %v", err)})
		return
	}

	attachment := domain.Attachment{
		FileName:    file.Filename,
		FileURL:     fileURL,
		FileType:    file.Header.Get("Content-Type"),
		FileSize:    file.Size,
		ReferenceID: refID,
	}

	if err := h.db.Create(&attachment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save to database: %v", err)})
		return
	}

	logger.Infof("Attachment saved to DB with ID: %s", attachment.ID)

	c.JSON(http.StatusOK, attachment)
}

// MyUploadImage is the JWT-gated authenticated equivalent of UploadImage.
// Uploads to a folder in sensitiveUploadFolders are stored privately (no
// public URL, no plain PutObject-then-return-URL) — the caller only gets
// the attachment's id back and must resolve a viewing URL via
// MyGetUploadURL, which is ownership-checked.
func (h *UploadHandler) MyUploadImage(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No image uploaded"})
		return
	}

	folder := c.PostForm("folder")
	if folder == "" {
		folder = "uploads"
	}
	isPrivate := sensitiveUploadFolders[folder]

	now := time.Now()
	uniqueID := uuid.New().String()
	ext := filepath.Ext(file.Filename)
	path := fmt.Sprintf("%s/%d/%02d/%s%s", folder, now.Year(), now.Month(), uniqueID, ext)

	attachment := domain.Attachment{
		FileName:   file.Filename,
		FileType:   file.Header.Get("Content-Type"),
		FileSize:   file.Size,
		StorageKey: path,
		UploaderID: &userID,
	}

	if isPrivate {
		if err := h.storage.PutObjectAt(c.Request.Context(), file, path); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upload: %v", err)})
			return
		}
		attachment.Visibility = "private"
	} else {
		fileURL, err := h.storage.UploadFile(c.Request.Context(), file, path)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upload: %v", err)})
			return
		}
		attachment.FileURL = fileURL
		attachment.Visibility = "public"
	}

	if err := h.db.Create(&attachment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save to database: %v", err)})
		return
	}

	c.JSON(http.StatusOK, attachment)
}

// MyGetUploadURL resolves a viewing URL for an attachment the caller owns —
// public attachments just return their permanent URL; private ones get a
// short-lived presigned URL, never a permanent one.
func (h *UploadHandler) MyGetUploadURL(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid attachment id"})
		return
	}

	var attachment domain.Attachment
	if err := h.db.First(&attachment, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Attachment not found"})
		return
	}

	if attachment.Visibility != "private" {
		c.JSON(http.StatusOK, gin.H{"url": attachment.FileURL})
		return
	}
	if attachment.UploaderID == nil || *attachment.UploaderID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this file"})
		return
	}

	url, err := h.storage.GetPresignedURL(c.Request.Context(), attachment.StorageKey, presignedURLTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve file URL"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "expires_in_seconds": int(presignedURLTTL.Seconds())})
}

// AdminGetAttachmentURL is the API-key-gated admin equivalent of
// MyGetUploadURL — no ownership check, since the admin panel is trusted to
// review any merchant's submitted verification documents.
func (h *UploadHandler) AdminGetAttachmentURL(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid attachment id"})
		return
	}

	var attachment domain.Attachment
	if err := h.db.First(&attachment, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Attachment not found"})
		return
	}

	if attachment.Visibility != "private" {
		c.JSON(http.StatusOK, gin.H{"url": attachment.FileURL})
		return
	}

	url, err := h.storage.GetPresignedURL(c.Request.Context(), attachment.StorageKey, presignedURLTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve file URL"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "expires_in_seconds": int(presignedURLTTL.Seconds())})
}

func (h *UploadHandler) ShowUploadPage(c *gin.Context) {
	html := `
<!DOCTYPE html>
<html>
<head>
    <title>Upload Image to R2</title>
    <style>
        body { font-family: sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; background: #f0f2f5; }
        .card { background: white; padding: 2rem; border-radius: 8px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); width: 400px; }
        h1 { margin-top: 0; color: #1a73e8; }
        input[type="file"] { margin: 1rem 0; width: 100%; }
        button { background: #1a73e8; color: white; border: none; padding: 0.75rem 1.5rem; border-radius: 4px; cursor: pointer; width: 100%; font-size: 1rem; }
        button:hover { background: #1557b0; }
        #result { margin-top: 1rem; word-break: break-all; font-size: 0.9rem; }
    </style>
</head>
<body>
    <div class="card">
        <h1>Upload Image</h1>
        <form id="uploadForm" enctype="multipart/form-data">
            <input type="file" name="image" accept="image/*" required>
            <input type="hidden" name="reference_id" value="">
            <button type="submit">Upload Now</button>
        </form>
        <div id="result"></div>
    </div>

    <script>
        document.getElementById('uploadForm').onsubmit = async (e) => {
            e.preventDefault();
            const formData = new FormData(e.target);
            const resDiv = document.getElementById('result');
            resDiv.innerHTML = 'Uploading...';
            
            try {
                const response = await fetch('/api/v1/upload', {
                    method: 'POST',
                    body: formData
                });
                const data = await response.json();
                if (response.ok) {
                    resDiv.innerHTML = '<strong>Success!</strong><br>URL: <a href="' + data.file_url + '" target="_blank">' + data.file_url + '</a>';
                } else {
                    resDiv.innerHTML = '<strong>Error:</strong> ' + (data.error || 'Unknown error');
                }
            } catch (err) {
                resDiv.innerHTML = '<strong>Error:</strong> ' + err.message;
            }
        };
    </script>
</body>
</html>
`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func (h *UploadHandler) DeleteFile(c *gin.Context) {
	fileURL := c.Query("url")
	if fileURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL is required"})
		return
	}

	err := h.storage.DeleteFile(c.Request.Context(), fileURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete from storage: %v", err)})
		return
	}

	// Also delete from attachments table if exists
	h.db.Where("file_url = ?", fileURL).Delete(&domain.Attachment{})

	c.JSON(http.StatusOK, gin.H{"message": "File deleted successfully"})
}
