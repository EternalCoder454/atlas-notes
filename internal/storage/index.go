package storage

import "atlas-notes/internal/checklist"

// noteID looks up the primary key for a note by its vault-relative path.
func (s *Store) noteID(rel string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM notes WHERE path = ?`, normalizeRel(rel)).Scan(&id)
	return id, err
}

// SetChecklistItems replaces the indexed checklist items for a note. Items are
// stored with a 1-based sort order, falling back to document position when an
// item carries no explicit order.
func (s *Store) SetChecklistItems(rel string, items []checklist.Item) error {
	id, err := s.noteID(rel)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM checklist_items WHERE note_id = ?`, id); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO checklist_items(note_id, text, priority, due_date, sort_order, checked)
		VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, it := range items {
		order := it.Order
		if order == 0 {
			order = i + 1
		}
		if _, err := stmt.Exec(id, it.Text, string(it.Priority), it.DueDate, order, b2i(it.Checked)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ChecklistItems returns a note's indexed checklist items ordered by sort_order.
func (s *Store) ChecklistItems(rel string) ([]checklist.Item, error) {
	id, err := s.noteID(rel)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT text, priority, due_date, sort_order, checked
		FROM checklist_items WHERE note_id = ? ORDER BY sort_order`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []checklist.Item
	for rows.Next() {
		var it checklist.Item
		var priority string
		var checked int
		if err := rows.Scan(&it.Text, &priority, &it.DueDate, &it.Order, &checked); err != nil {
			return nil, err
		}
		it.Priority = checklist.Priority(priority)
		it.Checked = checked != 0
		items = append(items, it)
	}
	return items, rows.Err()
}
