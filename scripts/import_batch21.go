//go:build ignore

// One-off bulk import: Student_Information_2025-2026-v2.csv -> CU Psychology,
// Batch 21 (session 2025-2026), plus promotion of three 2506… Batch-20
// irregulars into Batch 21 (session 2024-2025).
//
//	a) Serials 43-44: Md Nur Nobi (2506081003), Riya Moni (2506081037) already
//	   exist as CLAIMED Batch-20 profiles -> their rows are MOVED (batch_id,
//	   is_regular -> false, session -> 2024-2025). Name/email/user link untouched.
//	b) Serial 45: Tamim Hossen (2506081045) doesn't exist -> INSERTED as an
//	   irregular on session 2024-2025.
//	c) All other rows -> INSERTED as regular students, session 2025-2026,
//	   fresh unique 6-digit claim codes. Blank CSV rows are skipped. Gender
//	   has no schema field and is ignored.
//
// Usage:  go run scripts/import_batch21.go          (dry run — nothing written)
//         go run scripts/import_batch21.go --apply  (single transaction)
package main

import (
	"crypto/rand"
	"encoding/csv"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Resolved read-only against production on 2026-10-25 (see session notes).
const (
	cuUniversityID = "9bb8799c-482f-4654-a1a6-adbde2741186" // University of Chittagong
	cuPsychDeptID  = "cdc0f543-2bef-4c8d-9fdf-86d4c017ea84" // Department of Psychology
	batch21ID      = "58cd8aef-5d76-40ff-8d5c-7efc77d37771" // Batch 21

	session2025_26ID = "d7cb9906-7e6e-4a2d-aa76-6e989855c0c3" // "2025-2026" (regulars)
	session2024_25ID = "60ac107f-92e8-59d2-897c-7cc1581171ac" // "2024-2025" (irregulars)
)

var (
	regularSessionID   = uuid.MustParse(session2025_26ID)
	irregularSessionID = uuid.MustParse(session2024_25ID)
)

// The three promoted Batch-20 -> Batch-21 irregulars.
var promotedIDs = map[string]bool{
	"2506081003": true, // Md Nur Nobi    (claimed, Batch 20)
	"2506081037": true, // Riya Moni      (claimed, Batch 20)
	"2506081045": true, // Tamim Hossen   (not yet in DB)
}

type action struct {
	kind      string // "INSERT" | "MOVE"
	serial    string
	studentID string
	name      string
	email     string
	session   uuid.UUID
	regular   bool
	code      string // inserts only
	existing  *domain.Student
}

func main() {
	apply := len(os.Args) > 2 && os.Args[2] == "--apply"
	if len(os.Args) < 2 {
		log.Fatalf("usage: go run scripts/import_batch21.go <csv-path> [--apply]")
	}
	csvPath := os.Args[1]

	// --- resolve referenced rows; abort if any UUID is wrong -----------------
	if err := run(csvPath, apply); err != nil {
		log.Fatalf("import aborted: %v", err)
	}
}

func run(csvPath string, apply bool) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}

	// Sanity: every referenced row must exist as expected before touching data.
	for _, v := range []struct{ label, id string }{
		{"university", cuUniversityID}, {"department", cuPsychDeptID},
		{"batch", batch21ID}, {"session 2025-2026", session2025_26ID},
		{"session 2024-2025", session2024_25ID},
	} {
		var n int64
		if err := db.Table(entityTable(v.label)).Where("id = ?", v.id).Count(&n).Error; err != nil || n != 1 {
			return fmt.Errorf("%s %s not found", v.label, v.id)
		}
	}

	// Existing claim codes (unclaimed) so generated codes never collide.
	usedCodes := map[string]bool{}
	var claimedRows []struct{ VerificationCode string }
	if err := db.Model(&domain.Student{}).Unscoped().
		Where("is_claimed = false AND verification_code <> ''").
		Select("verification_code").Find(&claimedRows).Error; err != nil {
		return fmt.Errorf("load unclaimed codes: %w", err)
	}
	for _, r := range claimedRows {
		usedCodes[r.VerificationCode] = true
	}

	// Existing student profiles for this university+department, for dup checks
	// and for locating the claimed Batch-20 rows that get moved.
	var existing []domain.Student
	if err := db.Where("university_id = ? AND department_id = ?", cuUniversityID, cuPsychDeptID).
		Unscoped().Find(&existing).Error; err != nil {
		return fmt.Errorf("load existing students: %w", err)
	}

	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("open csv: %w", err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return fmt.Errorf("parse csv: %w", err)
	}
	if len(records) < 2 {
		return fmt.Errorf("csv has no data rows")
	}
	// Header: Serial No.,ID No.,Name,Email,Gender
	if !strings.Contains(records[0][1], "ID") || !strings.Contains(records[0][2], "Name") {
		return fmt.Errorf("unexpected header: %v", records[0])
	}

	var actions []action
	for _, rec := range records[1:] {
		if len(rec) < 4 {
			return fmt.Errorf("short row: %v", rec)
		}
		serial := strings.TrimSpace(rec[0])
		id := cleanID(rec[1])
		name := strings.TrimSpace(rec[2])
		email := strings.TrimSpace(rec[3])

		if id == "" && name == "" { // CSV row 35 is entirely blank
			log.Printf("SKIP  serial %s: blank row", serial)
			continue
		}
		if id == "" || name == "" {
			return fmt.Errorf("serial %s: partial row (id=%q name=%q) — refusing to guess", serial, id, name)
		}
		if !promotedIDs[id] && !strings.HasPrefix(id, "26") {
			return fmt.Errorf("serial %s: unexpected roll series %q", serial, id)
		}

		// Dup guard: same roll must not already exist in this department.
		for i := range existing {
			if existing[i].StudentID == id {
				if !promotedIDs[id] {
					return fmt.Errorf("serial %s: roll %s already exists (%s, batch %s)", serial, id, existing[i].Name, existing[i].BatchID)
				}
				// Promoted irregular with an existing (claimed) row: MOVE it.
				actions = append(actions, action{
					kind: "MOVE", serial: serial, studentID: id, name: name,
					session: irregularSessionID, regular: false, existing: &existing[i],
				})
				goto next
			}
		}
		// Fresh profile. The three 2506… IDs land here when absent (i.e. Tamim).
		{
			code, err := newUniqueCode(usedCodes)
			if err != nil {
				return fmt.Errorf("serial %s: %w", serial, err)
			}
			act := action{
				kind: "INSERT", serial: serial, studentID: id, name: name, email: email,
				session: regularSessionID, regular: true, code: code,
			}
			if promotedIDs[id] {
				act.session, act.regular = irregularSessionID, false
			}
			actions = append(actions, act)
		}
	next:
	}

	inserts, moves := 0, 0
	for _, a := range actions {
		if a.kind == "INSERT" {
			inserts++
		} else {
			moves++
		}
	}
	log.Printf("Plan: %d inserts (%d regular session 2025-2026), %d moves to Batch 21", inserts, inserts-moves, moves)
	for _, a := range actions {
		if a.kind == "MOVE" {
			log.Printf("MOVE  serial %-4s %s %-24s session 2024-2025, irregular — row %s (%s, claimed, Batch 20)", a.serial, a.studentID, a.name, a.existing.ID, a.existing.Email)
		} else {
			log.Printf("INSERT serial %-4s %s %-24s session %s, irregular=%v  code=%s", a.serial, a.studentID, a.name, sessionName(a), !a.regular, a.code)
		}
	}
	if !apply {
		log.Printf("DRY RUN — nothing written. Re-run with --apply to import.")
		return nil
	}

	applyErr := db.Transaction(func(tx *gorm.DB) error {
		for _, a := range actions {
			switch a.kind {
			case "MOVE":
				// Updater map drives updated_at automatically (GORM Updates).
				res := tx.Model(&domain.Student{}).Where("id = ?", a.existing.ID).Updates(map[string]interface{}{
					"batch_id":   batch21ID,
					"is_regular": false,
					"session_id": session2024_25ID,
				})
				if err := res.Error; err != nil {
					return fmt.Errorf("move %s: %w", a.existing.ID, err)
				}
			case "INSERT":
				row := domain.Student{
					StudentID:        a.studentID,
					Name:             a.name,
					Email:            a.email,
					BatchID:          parseID(batch21ID),
					DepartmentID:     parseID(cuPsychDeptID),
					UniversityID:     parseID(cuUniversityID),
					SessionID:        a.session,
					IsRegular:        true, // IsRegular has gorm "default:true"; a false value
					IsClaimed:        false, // is a zero value GORM skips on insert, so it is
					VerificationCode: a.code, // corrected right below for irregulars.
					Weight:           0,
				}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("insert %s (%s): %w", a.studentID, a.name, err)
				}
				if !a.regular {
					if err := tx.Model(&domain.Student{}).Where("id = ?", row.ID).
						Update("is_regular", false).Error; err != nil {
						return fmt.Errorf("mark irregular %s: %w", a.studentID, err)
					}
				}
			}
		}
		return nil
	})
	if applyErr != nil {
		return fmt.Errorf("apply: %w", applyErr)
	}

	// Verification-code sheet for distribution.
	outPath := strings.TrimSuffix(csvPath, ".csv") + "-claim-codes.csv"
	if err := writeCodeSheet(outPath, actions); err != nil {
		return fmt.Errorf("import done, but writing %s failed: %w", outPath, err)
	}
	log.Printf("DONE: %d inserted, %d moved. Claim codes written to %s", inserts, moves, outPath)
	return nil
}

func cleanID(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasSuffix(s, ".0") && len(s) > 2 {
		s = s[:len(s)-2]
	}
	return s
}

func newUniqueCode(used map[string]bool) (string, error) {
	for i := 0; i < 1000; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			return "", fmt.Errorf("generate code: %w", err)
		}
		code := fmt.Sprintf("%06d", n.Int64())
		if !used[code] {
			used[code] = true
			return code, nil
		}
	}
	return "", fmt.Errorf("could not find a unique code after 1000 tries")
}

func writeCodeSheet(path string, actions []action) error {
	var sb strings.Builder
	sb.WriteString("serial_no,student_id,name,email,verification_code\n")
	for _, a := range actions {
		if a.kind != "INSERT" {
			continue
		}
		sb.WriteString(strings.Join([]string{a.serial, a.studentID, csvField(a.name), csvField(a.email), a.code}, ",") + "\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func parseID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

func sessionName(a action) string {
	if a.regular {
		return "2025-2026"
	}
	return "2024-2025"
}

// entityTable maps sanity-check labels to their DB table.
func entityTable(label string) string {
	switch label {
	case "university":
		return "universities"
	case "department":
		return "departments"
	case "batch":
		return "batches"
	default:
		return "sessions"
	}
}
