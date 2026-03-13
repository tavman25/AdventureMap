package database

import (
	"database/sql"
	"log"
	"strings"
	"time"

	"travel-map/internal/models"

	_ "modernc.org/sqlite"
)

// DB wraps the sql.DB connection.
type DB struct {
	conn *sql.DB
}

// New opens (or creates) the SQLite database and runs migrations.
func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS pins (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_key   TEXT    NOT NULL DEFAULT 'password-admin',
			title       TEXT    NOT NULL,
			description TEXT    NOT NULL DEFAULT '',
			image_url   TEXT    NOT NULL DEFAULT '',
			latitude    REAL    NOT NULL,
			longitude   REAL    NOT NULL,
			color       TEXT    NOT NULL DEFAULT '#FF5722',
			icon        TEXT    NOT NULL DEFAULT '📍',
			visited_at  TEXT    NOT NULL DEFAULT '',
			created_at  DATETIME NOT NULL,
			updated_at  DATETIME NOT NULL
		);
	`)
	if err != nil {
		log.Printf("migration error: %v", err)
		return err
	}

	// Backfill for databases created before image_url existed.
	_, err = db.conn.Exec(`ALTER TABLE pins ADD COLUMN image_url TEXT NOT NULL DEFAULT ''`)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		log.Printf("migration alter error: %v", err)
		return err
	}

	_, err = db.conn.Exec(`ALTER TABLE pins ADD COLUMN owner_key TEXT NOT NULL DEFAULT 'password-admin'`)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		log.Printf("migration alter owner_key error: %v", err)
		return err
	}

	_, err = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_pins_owner_key_created_at ON pins(owner_key, created_at DESC)`)
	if err != nil {
		log.Printf("migration create index error: %v", err)
		return err
	}

	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS auth_events (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			provider       TEXT    NOT NULL,
			email          TEXT    NOT NULL DEFAULT '',
			identity       TEXT    NOT NULL DEFAULT '',
			ip             TEXT    NOT NULL DEFAULT '',
			forwarded_for  TEXT    NOT NULL DEFAULT '',
			host_name      TEXT    NOT NULL DEFAULT '',
			user_agent     TEXT    NOT NULL DEFAULT '',
			success        INTEGER NOT NULL DEFAULT 0,
			failure_reason TEXT    NOT NULL DEFAULT '',
			created_at     DATETIME NOT NULL
		);
	`)
	if err != nil {
		log.Printf("migration auth_events error: %v", err)
		return err
	}

	_, err = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_auth_events_created_at ON auth_events(created_at DESC)`)
	if err != nil {
		log.Printf("migration auth_events index error: %v", err)
		return err
	}
	return nil
}

// GetAllPins returns all pins ordered by creation time descending.
func (db *DB) GetAllPins(ownerKey string) ([]models.Pin, error) {
	query := `
		SELECT id, owner_key, title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at
		FROM pins`
	args := []interface{}{}
	if ownerKey != "" {
		query += ` WHERE owner_key = ?`
		args = append(args, ownerKey)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []models.Pin
	for rows.Next() {
		p, err := scanPin(rows)
		if err != nil {
			return nil, err
		}
		pins = append(pins, *p)
	}
	if pins == nil {
		pins = []models.Pin{}
	}
	return pins, nil
}

// GetPin returns a single pin by ID.
func (db *DB) GetPin(id int64, ownerKey string) (*models.Pin, error) {
	query := `
		SELECT id, owner_key, title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at
		FROM pins WHERE id = ?`
	args := []interface{}{id}
	if ownerKey != "" {
		query += ` AND owner_key = ?`
		args = append(args, ownerKey)
	}

	row := db.conn.QueryRow(query, args...)
	var p models.Pin
	var ca, ua string
	if err := row.Scan(&p.ID, &p.OwnerKey, &p.Title, &p.Description, &p.ImageURL, &p.Latitude, &p.Longitude,
		&p.Color, &p.Icon, &p.VisitedAt, &ca, &ua); err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &p, nil
}

// CreatePin inserts a new pin and returns its assigned ID.
func (db *DB) CreatePin(ownerKey string, p *models.Pin) error {
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	p.OwnerKey = ownerKey
	result, err := db.conn.Exec(`
		INSERT INTO pins (owner_key, title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ownerKey, p.Title, p.Description, p.ImageURL, p.Latitude, p.Longitude, p.Color, p.Icon, p.VisitedAt,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return err
	}
	p.ID, err = result.LastInsertId()
	return err
}

// UpdatePin modifies an existing pin.
func (db *DB) UpdatePin(ownerKey string, p *models.Pin) error {
	p.UpdatedAt = time.Now().UTC()
	query := `
		UPDATE pins SET title=?, description=?, image_url=?, latitude=?, longitude=?, color=?, icon=?, visited_at=?, updated_at=?
		WHERE id=?`
	args := []interface{}{p.Title, p.Description, p.ImageURL, p.Latitude, p.Longitude, p.Color, p.Icon, p.VisitedAt,
		p.UpdatedAt.Format(time.RFC3339), p.ID}
	if ownerKey != "" {
		query += ` AND owner_key=?`
		args = append(args, ownerKey)
	}
	result, err := db.conn.Exec(query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeletePin removes a pin by ID.
func (db *DB) DeletePin(id int64, ownerKey string) error {
	query := `DELETE FROM pins WHERE id = ?`
	args := []interface{}{id}
	if ownerKey != "" {
		query += ` AND owner_key = ?`
		args = append(args, ownerKey)
	}
	result, err := db.conn.Exec(query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// BulkCreatePins inserts multiple pins efficiently using a transaction.
func (db *DB) BulkCreatePins(ownerKey string, pins []models.Pin) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO pins (owner_key, title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	count := 0
	for _, p := range pins {
		_, err := stmt.Exec(ownerKey, p.Title, p.Description, p.ImageURL, p.Latitude, p.Longitude,
			p.Color, p.Icon, p.VisitedAt, now, now)
		if err != nil {
			tx.Rollback()
			return count, err
		}
		count++
	}
	return count, tx.Commit()
}

// UpdatePinImageByID sets a pin image URL by ID.
func (db *DB) UpdatePinImageByID(ownerKey string, id int64, imageURL string) (bool, error) {
	query := `UPDATE pins SET image_url=?, updated_at=? WHERE id=?`
	args := []interface{}{imageURL, time.Now().UTC().Format(time.RFC3339), id}
	if ownerKey != "" {
		query += ` AND owner_key=?`
		args = append(args, ownerKey)
	}
	result, err := db.conn.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// UpdatePinImageByTitle sets pin image URL for case-insensitive exact title matches.
func (db *DB) UpdatePinImageByTitle(ownerKey, title, imageURL string) (int64, error) {
	query := `UPDATE pins SET image_url=?, updated_at=? WHERE lower(title)=lower(?)`
	args := []interface{}{imageURL, time.Now().UTC().Format(time.RFC3339), title}
	if ownerKey != "" {
		query += ` AND owner_key=?`
		args = append(args, ownerKey)
	}
	result, err := db.conn.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// NormalizePinImageURLs repairs malformed stored image_url values, including concatenated URLs.
func (db *DB) NormalizePinImageURLs() (int64, error) {
	rows, err := db.conn.Query(`SELECT id, image_url FROM pins WHERE image_url <> ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	updated := int64(0)
	now := time.Now().UTC().Format(time.RFC3339)
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return updated, err
		}
		normalized := normalizeStoredImageURLs(raw)
		if normalized == raw {
			continue
		}
		if _, err := db.conn.Exec(`UPDATE pins SET image_url=?, updated_at=? WHERE id=?`, normalized, now, id); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, rows.Err()
}

func scanPin(scanner interface{ Scan(dest ...interface{}) error }) (*models.Pin, error) {
	var p models.Pin
	var ca, ua string
	if err := scanner.Scan(&p.ID, &p.OwnerKey, &p.Title, &p.Description, &p.ImageURL, &p.Latitude, &p.Longitude,
		&p.Color, &p.Icon, &p.VisitedAt, &ca, &ua); err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &p, nil
}

// LogAuthEvent stores an authentication attempt for audit purposes.
func (db *DB) LogAuthEvent(event models.AuthEvent) error {
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := db.conn.Exec(`
		INSERT INTO auth_events (provider, email, identity, ip, forwarded_for, host_name, user_agent, success, failure_reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.Provider, event.Email, event.Identity, event.IP, event.ForwardedFor, event.HostName, event.UserAgent, boolToInt(event.Success), event.FailureReason, createdAt.Format(time.RFC3339))
	return err
}

// ListAuthEvents returns recent authentication attempts ordered newest first.
func (db *DB) ListAuthEvents(limit int) ([]models.AuthEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.conn.Query(`
		SELECT id, provider, email, identity, ip, forwarded_for, host_name, user_agent, success, failure_reason, created_at
		FROM auth_events
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []models.AuthEvent{}
	for rows.Next() {
		var event models.AuthEvent
		var successInt int
		var createdAt string
		if err := rows.Scan(&event.ID, &event.Provider, &event.Email, &event.Identity, &event.IP, &event.ForwardedFor, &event.HostName, &event.UserAgent, &successInt, &event.FailureReason, &createdAt); err != nil {
			return nil, err
		}
		event.Success = successInt == 1
		event.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeStoredImageURLs(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parts := splitStoredImageURLs(trimmed)
	if len(parts) == 0 {
		parts = strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t'
		})
	}

	seen := map[string]struct{}{}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(candidate), "http://") && !strings.HasPrefix(strings.ToLower(candidate), "https://") {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		clean = append(clean, candidate)
	}

	return strings.Join(clean, "\n")
}

func splitStoredImageURLs(raw string) []string {
	var parts []string
	remaining := raw
	for {
		start := indexOfProtocol(remaining)
		if start < 0 {
			break
		}
		remaining = remaining[start:]
		next := indexOfNextProtocol(remaining)
		if next < 0 {
			parts = append(parts, remaining)
			break
		}
		parts = append(parts, remaining[:next])
		remaining = remaining[next:]
	}
	return parts
}

func indexOfProtocol(s string) int {
	httpIndex := strings.Index(strings.ToLower(s), "http://")
	httpsIndex := strings.Index(strings.ToLower(s), "https://")
	if httpIndex < 0 {
		return httpsIndex
	}
	if httpsIndex < 0 {
		return httpIndex
	}
	if httpIndex < httpsIndex {
		return httpIndex
	}
	return httpsIndex
}

func indexOfNextProtocol(s string) int {
	if len(s) == 0 {
		return -1
	}
	httpIndex := strings.Index(strings.ToLower(s[1:]), "http://")
	httpsIndex := strings.Index(strings.ToLower(s[1:]), "https://")
	if httpIndex >= 0 {
		httpIndex++
	}
	if httpsIndex >= 0 {
		httpsIndex++
	}
	if httpIndex < 0 {
		return httpsIndex
	}
	if httpsIndex < 0 {
		return httpIndex
	}
	if httpIndex < httpsIndex {
		return httpIndex
	}
	return httpsIndex
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}
