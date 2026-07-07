package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// chatIDKey uniquely identifies an external platform chat/conversation.
// It is used to map string identifiers (Mattermost channel/user IDs) to the
// integer chat IDs used by the rest of the storage layer.
type chatIDKey struct {
	platform   string
	externalID string
}

func (k chatIDKey) isValid() bool { return k.platform != "" && k.externalID != "" }

// GetOrCreateChatID returns the persistent integer chat ID for an external
// platform identifier. It creates a mapping row if one does not yet exist.
// This lets string-identifier transports (Mattermost) share the same integer
// keyed storage as Telegram.
func (s *DB) GetOrCreateChatID(ctx context.Context, platform, externalID string) (int64, error) {
	key := chatIDKey{platform: platform, externalID: externalID}
	if !key.isValid() {
		return 0, fmt.Errorf("platform and external_id are required")
	}

	// Optimistic path: existing mapping.
	var id int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM chat_id_mapping WHERE platform = ? AND external_id = ?",
		key.platform, key.externalID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("lookup chat id mapping: %w", err)
	}

	// Concurrent path: INSERT OR IGNORE, then re-SELECT.
	_, err = s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO chat_id_mapping (platform, external_id, created_at) VALUES (?, ?, ?)",
		key.platform, key.externalID, time.Now().UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("create chat id mapping: %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		"SELECT id FROM chat_id_mapping WHERE platform = ? AND external_id = ?",
		key.platform, key.externalID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("lookup chat id mapping after insert: %w", err)
	}
	return id, nil
}

// ResolveChatID returns the platform/external identifier associated with a
// previously mapped integer chat ID. It is used by the notification layer to
// deliver proactive messages back to the right transport conversation.
func (s *DB) ResolveChatID(ctx context.Context, id int64) (platform, externalID string, err error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT platform, external_id FROM chat_id_mapping WHERE id = ?",
		id,
	)
	if err := row.Scan(&platform, &externalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("chat id %d not mapped", id)
		}
		return "", "", fmt.Errorf("resolve chat id: %w", err)
	}
	return platform, externalID, nil
}
