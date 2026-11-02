package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Session is a global admission-period label — "2017-2018" at public
// universities, "Spring 2023" at private ones. It means the same thing at
// every university, so there is exactly one row per period system-wide.
// Which sessions apply to a department is decided by its batches
// (Batch.Sessions / batch_sessions), not by the session itself.
type Session struct {
	Base
	Kind      string `gorm:"size:20;not null;default:'academic_year'" json:"kind"` // SessionKindAcademicYear | SessionKindSemester
	StartYear int    `gorm:"not null;default:0" json:"start_year"`                 // 2017 for "2017-2018" and "Spring 2017"
	Term      string `gorm:"size:20;not null;default:''" json:"term"`              // "" for academic years; spring/summer/fall/autumn/winter for semesters
	Name      string `gorm:"size:50;not null" json:"name"`                         // derived: "2017-2018" or "Spring 2017"
	SortKey   int    `gorm:"not null;default:0;index" json:"sort_key"`             // derived: StartYear*10 + term order, for chronological ordering
}

const (
	SessionKindAcademicYear = "academic_year"
	SessionKindSemester     = "semester"
)

// sessionTermOrder is the within-year order of semester terms. Fall and
// autumn are the same slot under different names (CIU says "Autumn").
var sessionTermOrder = map[string]int{"spring": 1, "summer": 2, "fall": 3, "autumn": 3, "winter": 4}

var (
	academicYearName = regexp.MustCompile(`^(\d{4})-(\d{4})$`)
	semesterName     = regexp.MustCompile(`^([A-Za-z]+) (\d{4})$`)
)

// BeforeSave normalizes and validates the session, and derives Name and
// SortKey from Kind/StartYear/Term so every client gets the same format.
// A client may send just a name ("2017-2018", "Spring 2023") instead of
// the structured fields; it is parsed here.
func (s *Session) BeforeSave(_ *gorm.DB) error {
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	s.Term = strings.ToLower(strings.TrimSpace(s.Term))
	s.Name = strings.TrimSpace(s.Name)

	if s.StartYear == 0 && s.Name != "" {
		if m := academicYearName.FindStringSubmatch(s.Name); m != nil {
			start, _ := strconv.Atoi(m[1])
			end, _ := strconv.Atoi(m[2])
			if end != start+1 {
				return errors.New("invalid session: an academic year must span consecutive years, e.g. 2019-2020")
			}
			s.Kind, s.StartYear, s.Term = SessionKindAcademicYear, start, ""
		} else if m := semesterName.FindStringSubmatch(s.Name); m != nil {
			s.Kind, s.Term = SessionKindSemester, strings.ToLower(m[1])
			s.StartYear, _ = strconv.Atoi(m[2])
		}
	}
	if s.Kind == "" {
		s.Kind = SessionKindAcademicYear
	}

	if s.StartYear < 1900 || s.StartYear > 2200 {
		return errors.New("invalid session: start_year must be a 4-digit year")
	}

	switch s.Kind {
	case SessionKindAcademicYear:
		s.Term = ""
		s.Name = fmt.Sprintf("%d-%d", s.StartYear, s.StartYear+1)
		s.SortKey = s.StartYear * 10
	case SessionKindSemester:
		order, ok := sessionTermOrder[s.Term]
		if !ok {
			return errors.New("invalid session: term must be one of spring, summer, fall, autumn, winter")
		}
		s.Name = fmt.Sprintf("%s %d", strings.ToUpper(s.Term[:1])+s.Term[1:], s.StartYear)
		s.SortKey = s.StartYear*10 + order
	default:
		return errors.New("invalid session: kind must be academic_year or semester")
	}
	return nil
}

// BatchHasSession reports whether sessionID is one of batch batchID's
// sessions — the rule that keeps a student's session consistent with
// their batch now that sessions themselves are global.
func BatchHasSession(db *gorm.DB, batchID, sessionID uuid.UUID) (bool, error) {
	var count int64
	err := db.Table("batch_sessions").
		Joins("JOIN sessions ON sessions.id = batch_sessions.session_id AND sessions.deleted_at IS NULL").
		Where("batch_sessions.batch_id = ? AND batch_sessions.session_id = ?", batchID, sessionID).
		Count(&count).Error
	return count > 0, err
}
