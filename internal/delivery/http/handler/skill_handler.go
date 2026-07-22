package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SkillHandler struct {
	repo domain.SkillRepository
}

func NewSkillHandler(repo domain.SkillRepository) *SkillHandler {
	return &SkillHandler{repo: repo}
}

// GetAllSkills is the admin listing — every skill, any publish status.
func (h *SkillHandler) GetAllSkills(c *gin.Context) {
	skills, err := h.repo.GetAllSkills(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch skills"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": skills})
}

func (h *SkillHandler) GetSkillByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid skill id"})
		return
	}
	skill, err := h.repo.GetSkillByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Skill not found"})
		return
	}
	c.JSON(http.StatusOK, skill)
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var skill domain.Skill
	if err := c.ShouldBindJSON(&skill); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreateSkill(c.Request.Context(), &skill); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create skill"})
		return
	}
	c.JSON(http.StatusOK, skill)
}

func (h *SkillHandler) UpdateSkill(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid skill id"})
		return
	}
	var skill domain.Skill
	if err := c.ShouldBindJSON(&skill); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	skill.ID = id
	if err := h.repo.UpdateSkill(c.Request.Context(), &skill); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update skill"})
		return
	}
	c.JSON(http.StatusOK, skill)
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid skill id"})
		return
	}
	if err := h.repo.DeleteSkill(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete skill"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Skill deleted"})
}

// GetSkillsByLocation is the app-facing endpoint: published skills that
// are global (no targets) or targeted to this university/department.
func (h *SkillHandler) GetSkillsByLocation(c *gin.Context) {
	universityID, _ := uuid.Parse(c.Query("university_id"))
	departmentID, _ := uuid.Parse(c.Query("department_id"))

	skills, err := h.repo.GetSkillsByLocation(c.Request.Context(), universityID, departmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch skills"})
		return
	}
	c.JSON(http.StatusOK, skills)
}
