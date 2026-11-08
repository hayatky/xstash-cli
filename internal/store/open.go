package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Open creates a new private database, or validates an existing schema-v1
// database without repairing it. Only :memory: creates an in-memory database.
func Open(path string) (*Store, error) { return openDatabase(path, false) }

// OpenExisting requires a regular file with the required schema-v1 structures.
// It never creates a file or initializes or repairs an existing database.
func OpenExisting(path string) (*Store, error) { return openDatabase(path, true) }

func openDatabase(path string, existingOnly bool) (*Store, error) {
	fresh := path == ":memory:"
	dsn := path
	if fresh && existingOnly {
		return nil, fmt.Errorf("existing database must be a regular file")
	}
	if !fresh {
		if !existingOnly {
			f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				fresh = true
				if err = f.Close(); err != nil {
					return nil, err
				}
			} else if !os.IsExist(err) {
				return nil, err
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("open existing database: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("database must be a regular file (not a symlink)")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		// Escape literal ?, # and % in filenames. mode=rw also prevents SQLite from
		// creating a replacement if the file disappears after our filesystem check.
		uriPath := filepath.ToSlash(absolute)
		if !strings.HasPrefix(uriPath, "/") {
			uriPath = "/" + uriPath
		}
		dsn = (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=rw"}).String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fail(fmt.Errorf("open database: %w", err))
	}
	if fresh {
		if _, err = db.Exec(schemaV1); err != nil {
			return fail(fmt.Errorf("initialize database: %w", err))
		}
	} else if err = validateSchema(db); err != nil {
		return fail(err)
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			return fail(err)
		}
	}
	return &Store{DB: db}, nil
}

type columnInfo struct {
	ID                 int
	Name, Type         string
	NotNull            int
	Default            sql.NullString
	PrimaryKey, Hidden int
}

func tableColumns(db *sql.DB, table string) ([]columnInfo, error) {
	rows, err := db.Query(`SELECT cid,name,type,"notnull",dflt_value,pk,hidden FROM pragma_table_xinfo(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []columnInfo
	for rows.Next() {
		var c columnInfo
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.NotNull, &c.Default, &c.PrimaryKey, &c.Hidden); err != nil {
			return nil, err
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}

func validateSchema(db *sql.DB) error {
	var count, valid int
	if err := db.QueryRow(`SELECT count(*),coalesce(sum(typeof(version)='integer' AND version=1),0) FROM xstash_go_meta`).Scan(&count, &valid); err != nil || count != 1 || valid != 1 {
		return fmt.Errorf("unsupported or invalid xstash Go database schema metadata")
	}
	// Derive required column layouts from the same versioned schema used for
	// creation. This detects missing tables, columns, keys and altered defaults
	// without running DDL against the user's database.
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer reference.Close()
	reference.SetMaxOpenConns(1)
	if _, err := reference.Exec(schemaV1); err != nil {
		return err
	}
	rows, err := reference.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range tables {
		var kind string
		if err := db.QueryRow(`SELECT type FROM sqlite_schema WHERE name=?`, name).Scan(&kind); err != nil || kind != "table" {
			return fmt.Errorf("invalid xstash Go database: missing table %s", name)
		}
		want, err := tableColumns(reference, name)
		if err != nil {
			return err
		}
		got, err := tableColumns(db, name)
		if err != nil {
			return fmt.Errorf("inspect table %s: %w", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("invalid xstash Go database: incompatible table %s", name)
		}
	}
	// Validate foreign keys and indexes too: column layouts alone do not prove
	// that relation integrity or uniqueness constraints are still enforced.
	for _, table := range tables {
		for _, query := range []string{
			`SELECT id,seq,"table","from","to",on_update,on_delete,match FROM pragma_foreign_key_list(?) ORDER BY id,seq`,
			`SELECT name,"unique",origin,partial FROM pragma_index_list(?) ORDER BY name`,
		} {
			want, err := schemaRows(reference, query, table)
			if err != nil {
				return err
			}
			got, err := schemaRows(db, query, table)
			if err != nil {
				return fmt.Errorf("inspect table %s constraints: %w", table, err)
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("invalid xstash Go database: incompatible constraints for %s", table)
			}
		}
	}
	for _, index := range []string{"idx_relations_seen", "idx_requests_date"} {
		query := `SELECT seqno,cid,name,desc,coll,key FROM pragma_index_xinfo(?) ORDER BY seqno`
		want, err := schemaRows(reference, query, index)
		if err != nil {
			return err
		}
		got, err := schemaRows(db, query, index)
		if err != nil {
			return fmt.Errorf("inspect index %s: %w", index, err)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("invalid xstash Go database: incompatible index %s", index)
		}
	}
	return nil
}

// CheckIntegrity scans the database for storage and foreign-key inconsistencies.
// Call this explicitly for diagnostics, rather than on every ordinary command.
// It does not repair or change the database.
func (s *Store) CheckIntegrity() error {
	db := s.DB
	var check string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil {
		return fmt.Errorf("check database: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("database integrity check failed")
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check database foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("database foreign key integrity check failed")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check database foreign keys: %w", err)
	}
	return nil
}

// schemaRows reads only SQLite schema metadata. The values are SQLite scalar
// values, so they can be compared without relying on CREATE SQL formatting.
func schemaRows(db *sql.DB, query, name string) ([][]any, error) {
	rows, err := db.Query(query, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		result = append(result, values)
	}
	return result, rows.Err()
}
