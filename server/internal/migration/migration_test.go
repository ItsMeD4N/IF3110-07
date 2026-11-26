package migration

import (
	"strings"
	"testing"
)

func TestCheckTransactionControl(t *testing.T) {
	allowed := []string{
		"CREATE TABLE a (id int); INSERT INTO a VALUES (1);",
		"-- COMMIT di komentar\nCREATE TABLE a (id int)",
		"/* BEGIN; COMMIT; */ SELECT 1",
		"INSERT INTO log (msg) VALUES ('commit; rollback;')",
		"INSERT INTO log (msg) VALUES (E'it\\'s; commit')",
		`CREATE TABLE "commit" (x int)`,
		"DO $$ BEGIN RAISE NOTICE 'x'; END $$;",
		"CREATE FUNCTION f() RETURNS int AS $body$ BEGIN RETURN 1; END; $body$ LANGUAGE plpgsql;",
		"SAVEPOINT s1; UPDATE a SET id = 2; ROLLBACK TO SAVEPOINT s1;",
		"SELECT CASE WHEN true THEN 1 END;",
		"  ;  ; ",
	}
	for _, sql := range allowed {
		if err := checkTransactionControl(sql); err != nil {
			t.Errorf("SQL valid ditolak: %q: %v", sql, err)
		}
	}

	forbidden := map[string]string{
		"CREATE TABLE a (x int); COMMIT; SELECT 1":        "COMMIT",
		"begin; create table a (x int);":                  "BEGIN",
		"START TRANSACTION; SELECT 1":                     "START",
		"UPDATE a SET x = 1; rollback":                    "ROLLBACK",
		"SELECT 1; END;":                                  "END",
		"SELECT 1; ABORT":                                 "ABORT",
		"PREPARE TRANSACTION 'x'":                         "PREPARE",
		"SELECT '$$'; COMMIT":                             "COMMIT",
		"/* c */ -- c\n  Commit  ":                        "COMMIT",
		"INSERT INTO t VALUES ('a''b'); COMMIT AND CHAIN": "COMMIT",
	}
	for sql, word := range forbidden {
		err := checkTransactionControl(sql)
		if err == nil || !strings.Contains(err.Error(), word) {
			t.Errorf("SQL %q harus ditolak karena %s, err = %v", sql, word, err)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := validate("", ""); err == nil {
		t.Error("SQL kosong harus ditolak")
	}
	if err := validate(strings.Repeat("n", maxNameLen+1), "SELECT 1"); err == nil {
		t.Error("nama terlalu panjang harus ditolak")
	}
	if err := validate("", strings.Repeat("x", maxSQLBytes+1)); err == nil {
		t.Error("SQL lebih dari 1 MiB harus ditolak")
	}
	if err := validate("ok", "SELECT 1"); err != nil {
		t.Errorf("input valid ditolak: %v", err)
	}
}
