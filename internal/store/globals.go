package store

import "time"

// SetGroupVar upserts a group-global variable: one that is true of the whole
// group, in every workflow and every repo.
//
// This is the bottom layer, so it is a *default* rather than a binding. When
// a value stops being universal — production moves to its own cluster — any
// layer above simply overrides it, and nothing has to be unwired first.
func (s *Store) SetGroupVar(groupID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO group_vars(group_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(group_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		groupID, key, blob, time.Now().Unix())
	return err
}

// GroupVars returns the group-global layer, decrypted.
func (s *Store) GroupVars(groupID int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM group_vars WHERE group_id = ?`, groupID)
}

// ReplaceGroupVars makes the group-global layer exactly vars. The caller is
// handing over the complete, intended layer (an edit session, a sync), so a
// key absent from vars is deleted rather than merged — a partial map is
// never treated as a set of additions.
func (s *Store) ReplaceGroupVars(groupID int64, vars map[string]string) error {
	for key := range vars {
		if err := checkKey(key); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM group_vars WHERE group_id = ?`, groupID); err != nil {
		return err
	}

	now := time.Now().Unix()
	for key, value := range vars {
		blob, err := s.cipher.Encrypt(value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO group_vars(group_id, key, value, updated_at) VALUES (?, ?, ?, ?)`,
			groupID, key, blob, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
