package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrQuickReplyConflict = errors.New("quick reply was changed or removed")

type QuickReply struct {
	ID        int       `json:"id"`
	CompanyID int       `json:"companyId"`
	Shortcut  string    `json:"shortcut"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func ListQuickReplies(ctx context.Context, companyID int) ([]QuickReply, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, "companyId", shortcut, title, content, revision, "createdAt", "updatedAt"
		FROM quick_replies WHERE "companyId" = $1 ORDER BY shortcut, id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	replies := []QuickReply{}
	for rows.Next() {
		var reply QuickReply
		if err := rows.Scan(&reply.ID, &reply.CompanyID, &reply.Shortcut, &reply.Title, &reply.Content, &reply.Revision, &reply.CreatedAt, &reply.UpdatedAt); err != nil {
			return nil, err
		}
		replies = append(replies, reply)
	}
	return replies, rows.Err()
}

func CreateQuickReply(ctx context.Context, reply QuickReply) (QuickReply, error) {
	err := db.QueryRowContext(ctx, `INSERT INTO quick_replies ("companyId", shortcut, title, content)
		VALUES ($1, $2, $3, $4) RETURNING id, revision, "createdAt", "updatedAt"`,
		reply.CompanyID, reply.Shortcut, reply.Title, reply.Content).Scan(&reply.ID, &reply.Revision, &reply.CreatedAt, &reply.UpdatedAt)
	return reply, err
}

func UpdateQuickReply(ctx context.Context, reply *QuickReply) error {
	err := db.QueryRowContext(ctx, `UPDATE quick_replies SET shortcut = $1, title = $2, content = $3,
		revision = revision + 1, "updatedAt" = now() WHERE id = $4 AND "companyId" = $5 AND revision = $6
		RETURNING revision, "createdAt", "updatedAt"`, reply.Shortcut, reply.Title, reply.Content, reply.ID, reply.CompanyID, reply.Revision).Scan(&reply.Revision, &reply.CreatedAt, &reply.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrQuickReplyConflict
	}
	return err
}

func DeleteQuickReply(ctx context.Context, id, companyID int) error {
	result, err := db.ExecContext(ctx, `DELETE FROM quick_replies WHERE id = $1 AND "companyId" = $2`, id, companyID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
