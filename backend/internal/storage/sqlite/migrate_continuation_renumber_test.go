package sqlite

import (
	"strconv"
	"strings"
	"testing"
)

func TestMigrateRecognizesRenumberedContinuation(t *testing.T) {
	for _, version := range []int64{176, 189, 190} {
		t.Run(strconv.FormatInt(version, 10), func(t *testing.T) {
			baseVersion := version
			if version == 190 {
				baseVersion = 189
			}
			db := openMigratedDatabaseCopyNoForeignKeys(t, baseVersion)
			mustExec(t, db, `ALTER TABLE conversation_messages ADD COLUMN continuation INTEGER NOT NULL DEFAULT 0 CHECK (continuation IN (0, 1))`)
			if version == 190 {
				mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (190, 1)`)
			}
			if version == 176 {
				mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (177, 1)`)
			}
			mustExec(t, db, `INSERT INTO conversation_messages (id, conversation_id, branch_id, sequence, role, origin, text, continuation, created_at, updated_at) VALUES ('preserved', 'conversation', 'branch', 1, 'user', 'human', 'Continue from where you stopped.', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			for startup := 0; startup < 2; startup++ {
				if err := migrate(db); err != nil {
					t.Fatalf("startup %d: %v", startup, err)
				}
			}
			var text string
			var continuation int
			if err := db.QueryRow(`SELECT text, continuation FROM conversation_messages WHERE id = 'preserved'`).Scan(&text, &continuation); err != nil {
				t.Fatal(err)
			}
			if text != "Continue from where you stopped." || continuation != 1 {
				t.Fatalf("message changed: %q, %d", text, continuation)
			}
			var indexSQL string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'idx_review_run_session_pr_sha_harness'`).Scan(&indexSQL); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.ToLower(indexSQL), "verdict") {
				t.Fatal("main's review rerun migration was skipped")
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 192 AND is_applied = 1`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			var hibernationColumn int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'hibernated_at'`).Scan(&hibernationColumn); err != nil {
				t.Fatal(err)
			}
			if hibernationColumn != 1 {
				t.Fatal("main's hibernation migration was skipped")
			}
			if count != 1 {
				t.Fatalf("canonical migration entries = %d, want 1", count)
			}
		})
	}
}
