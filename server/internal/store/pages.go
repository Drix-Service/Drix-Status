package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrConflict is returned on unique violations (slug or domain taken).
var ErrConflict = errors.New("already in use")

func conflict(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return notFound(err)
}

// Page is a public status page.
type Page struct {
	ID           int64     `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	LogoURL      string    `json:"logo_url"`
	WebsiteURL   string    `json:"website_url"`
	AccentColor  string    `json:"accent_color"`
	FooterText   string    `json:"footer_text"`
	ShowLatency  bool      `json:"show_latency"`
	Language     string    `json:"language"` // default for visitors, e.g. "en"
	CustomDomain *string   `json:"custom_domain"`
	IsDefault    bool      `json:"is_default"`
	Position     int       `json:"position"`
	CreatedAt    time.Time `json:"created_at"`
}

// Section groups monitors on one page.
type Section struct {
	ID          int64         `json:"id"`
	PageID      int64         `json:"page_id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Position    int           `json:"position"`
	Collapsed   bool          `json:"collapsed"`
	Monitors    []PageMonitor `json:"monitors"`
}

// PageMonitor places a monitor in a section, optionally renamed for that page.
type PageMonitor struct {
	MonitorID   int64   `json:"monitor_id"`
	DisplayName *string `json:"display_name"`
}

const pageCols = `id, slug, name, description, logo_url, website_url, accent_color, footer_text,
	show_latency, custom_domain, is_default, position, created_at, language`

func scanPage(row pgx.Row) (Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.LogoURL, &p.WebsiteURL, &p.AccentColor,
		&p.FooterText, &p.ShowLatency, &p.CustomDomain, &p.IsDefault, &p.Position, &p.CreatedAt, &p.Language)
	return p, err
}

func (s *Store) ListPages(ctx context.Context) ([]Page, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+pageCols+` FROM status_pages ORDER BY is_default DESC, position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPage(ctx context.Context, id int64) (Page, error) {
	p, err := scanPage(s.DB.QueryRow(ctx, `SELECT `+pageCols+` FROM status_pages WHERE id=$1`, id))
	return p, notFound(err)
}

func (s *Store) UpdatePage(ctx context.Context, p Page) (Page, error) {
	out, err := scanPage(s.DB.QueryRow(ctx, `
		UPDATE status_pages SET slug=$2, name=$3, description=$4, logo_url=$5, website_url=$6,
			accent_color=$7, footer_text=$8, show_latency=$9, custom_domain=$10, language=$11
		WHERE id=$1 RETURNING `+pageCols,
		p.ID, p.Slug, p.Name, p.Description, p.LogoURL, p.WebsiteURL, p.AccentColor, p.FooterText, p.ShowLatency, p.CustomDomain, p.Language))
	return out, conflict(err)
}

// ListSections returns every section with its monitors, for all pages.
func (s *Store) ListSections(ctx context.Context) ([]Section, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT s.id, s.page_id, s.name, s.description, s.position, s.collapsed,
		       COALESCE(json_agg(json_build_object('monitor_id', pm.monitor_id, 'display_name', pm.display_name)
		                ORDER BY pm.position, pm.monitor_id) FILTER (WHERE pm.monitor_id IS NOT NULL), '[]')
		FROM page_sections s LEFT JOIN page_monitors pm ON pm.section_id = s.id
		GROUP BY s.id ORDER BY s.page_id, s.position, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Section{}
	for rows.Next() {
		var sec Section
		if err := rows.Scan(&sec.ID, &sec.PageID, &sec.Name, &sec.Description, &sec.Position, &sec.Collapsed, &sec.Monitors); err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// SaveLayout replaces a page's sections and their monitors in one transaction.
// Sections with an ID are kept (and updated); missing ones are deleted.
func (s *Store) SaveLayout(ctx context.Context, pageID int64, sections []Section) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	keep := []int64{}
	for _, sec := range sections {
		if sec.ID > 0 {
			keep = append(keep, sec.ID)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM page_sections WHERE page_id=$1 AND NOT (id = ANY($2))`, pageID, keep); err != nil {
		return err
	}
	for i, sec := range sections {
		id := sec.ID
		if id > 0 {
			tag, err := tx.Exec(ctx, `UPDATE page_sections SET name=$3, description=$4, position=$5, collapsed=$6
				WHERE id=$1 AND page_id=$2`, id, pageID, sec.Name, sec.Description, i, sec.Collapsed)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		} else if err := tx.QueryRow(ctx, `INSERT INTO page_sections (page_id, name, description, position, collapsed)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`, pageID, sec.Name, sec.Description, i, sec.Collapsed).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM page_monitors WHERE section_id=$1`, id); err != nil {
			return err
		}
		for j, pm := range sec.Monitors {
			if _, err := tx.Exec(ctx, `INSERT INTO page_monitors (section_id, monitor_id, position, display_name)
				VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, pm.MonitorID, j, pm.DisplayName); err != nil {
				return notFound(err)
			}
		}
	}
	return tx.Commit(ctx)
}

// AddMonitorToSection appends a monitor to a section (used when creating monitors).
func (s *Store) AddMonitorToSection(ctx context.Context, sectionID, monitorID int64) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO page_monitors (section_id, monitor_id, position)
		VALUES ($1, $2, COALESCE((SELECT MAX(position)+1 FROM page_monitors WHERE section_id=$1), 0))
		ON CONFLICT DO NOTHING`, sectionID, monitorID)
	return err
}
