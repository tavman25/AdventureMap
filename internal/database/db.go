package database

import (
	"database/sql"
	"log"
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
			title       TEXT    NOT NULL,
			description TEXT    NOT NULL DEFAULT '',
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
	}
	return err
}

// GetAllPins returns all pins ordered by creation time descending.
func (db *DB) GetAllPins() ([]models.Pin, error) {
	rows, err := db.conn.Query(`
		SELECT id, title, description, latitude, longitude, color, icon, visited_at, created_at, updated_at
		FROM pins ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []models.Pin
	for rows.Next() {
		var p models.Pin
		var ca, ua string
		if err := rows.Scan(&p.ID, &p.Title, &p.Description, &p.Latitude, &p.Longitude,
			&p.Color, &p.Icon, &p.VisitedAt, &ca, &ua); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		pins = append(pins, p)
	}
	if pins == nil {
		pins = []models.Pin{}
	}
	return pins, nil
}

// GetPin returns a single pin by ID.
func (db *DB) GetPin(id int64) (*models.Pin, error) {
	row := db.conn.QueryRow(`
		SELECT id, title, description, latitude, longitude, color, icon, visited_at, created_at, updated_at
		FROM pins WHERE id = ?
	`, id)
	var p models.Pin
	var ca, ua string
	if err := row.Scan(&p.ID, &p.Title, &p.Description, &p.Latitude, &p.Longitude,
		&p.Color, &p.Icon, &p.VisitedAt, &ca, &ua); err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &p, nil
}

// CreatePin inserts a new pin and returns its assigned ID.
func (db *DB) CreatePin(p *models.Pin) error {
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	result, err := db.conn.Exec(`
		INSERT INTO pins (title, description, latitude, longitude, color, icon, visited_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.Title, p.Description, p.Latitude, p.Longitude, p.Color, p.Icon, p.VisitedAt,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return err
	}
	p.ID, err = result.LastInsertId()
	return err
}

// UpdatePin modifies an existing pin.
func (db *DB) UpdatePin(p *models.Pin) error {
	p.UpdatedAt = time.Now().UTC()
	_, err := db.conn.Exec(`
		UPDATE pins SET title=?, description=?, latitude=?, longitude=?, color=?, icon=?, visited_at=?, updated_at=?
		WHERE id=?
	`, p.Title, p.Description, p.Latitude, p.Longitude, p.Color, p.Icon, p.VisitedAt,
		p.UpdatedAt.Format(time.RFC3339), p.ID)
	return err
}

// DeletePin removes a pin by ID.
func (db *DB) DeletePin(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM pins WHERE id = ?`, id)
	return err
}

// BulkCreatePins inserts multiple pins efficiently using a transaction.
func (db *DB) BulkCreatePins(pins []models.Pin) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO pins (title, description, latitude, longitude, color, icon, visited_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	count := 0
	for _, p := range pins {
		_, err := stmt.Exec(p.Title, p.Description, p.Latitude, p.Longitude,
			p.Color, p.Icon, p.VisitedAt, now, now)
		if err != nil {
			tx.Rollback()
			return count, err
		}
		count++
	}
	return count, tx.Commit()
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}
