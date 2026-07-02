package content

import (
	"context"
	"database/sql"
	"math"
	"strconv"
)

type Rating struct {
	ID         int     `json:"id"`
	ArticleID  int     `json:"article_id"`
	UserID     *int    `json:"user_id,omitempty"`
	AuthorName string  `json:"author_name"`
	Score      float64 `json:"score"`
	CreatedAt  string  `json:"created_at"`
}

func VoterKey(userID *int, authorName string) string {
	if userID != nil {
		return "u:" + strconv.Itoa(*userID)
	}
	return "n:" + authorName
}

type RatingSummary struct {
	Average     float64  `json:"average_score"`
	TotalVoters int      `json:"total_voters"`
	UserScore   *float64 `json:"user_score,omitempty"`
}

func GetRatingSummaryCtx(ctx context.Context, db *sql.DB, articleID int, voterKey string) (*RatingSummary, error) {
	rs := &RatingSummary{}
	var avg sql.NullFloat64
	var total int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), AVG(score) FROM ratings WHERE article_id = ?`, articleID,
	).Scan(&total, &avg)
	if err != nil {
		return nil, err
	}
	rs.TotalVoters = total
	if avg.Valid {
		rs.Average = math.Round(avg.Float64*100) / 100
	}
	if voterKey != "" {
		var score sql.NullFloat64
		err := db.QueryRowContext(ctx,
			`SELECT score FROM ratings WHERE article_id = ? AND voter_key = ?`,
			articleID, voterKey,
		).Scan(&score)
		if err == nil && score.Valid {
			s := math.Round(score.Float64*100) / 100
			rs.UserScore = &s
		}
	}
	return rs, nil
}

// Deprecated: Use GetRatingSummaryCtx instead.
func GetRatingSummary(db *sql.DB, articleID int, voterKey string) (*RatingSummary, error) {
	return GetRatingSummaryCtx(context.TODO(), db, articleID, voterKey)
}

func SetRatingCtx(ctx context.Context, db *sql.DB, articleID int, userID *int, authorName string, score float64) (*RatingSummary, error) {
	if score < -1 {
		score = -1
	}
	if score > 1 {
		score = 1
	}
	score = math.Round(score*100) / 100
	voter := VoterKey(userID, authorName)

	_, err := db.ExecContext(ctx,
		`INSERT INTO ratings (article_id, user_id, author_name, voter_key, score)
		 VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE score = VALUES(score), updated_at = CURRENT_TIMESTAMP`,
		articleID, userID, authorName, voter, score,
	)
	if err != nil {
		return nil, err
	}
	return GetRatingSummaryCtx(ctx, db, articleID, voter)
}

// Deprecated: Use SetRatingCtx instead.
func SetRating(db *sql.DB, articleID int, userID *int, authorName string, score float64) (*RatingSummary, error) {
	return SetRatingCtx(context.TODO(), db, articleID, userID, authorName, score)
}

func DeleteRatingCtx(ctx context.Context, db *sql.DB, articleID int, voterKey string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM ratings WHERE article_id = ? AND voter_key = ?`,
		articleID, voterKey,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Deprecated: Use DeleteRatingCtx instead.
func DeleteRating(db *sql.DB, articleID int, voterKey string) (bool, error) {
	return DeleteRatingCtx(context.TODO(), db, articleID, voterKey)
}

func ListRatingsCtx(ctx context.Context, db *sql.DB, articleID int) ([]Rating, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, article_id, COALESCE(user_id, 0) AS uid, author_name, score, COALESCE(created_at, '') FROM ratings
		 WHERE article_id = ? ORDER BY created_at DESC`,
		articleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ratings []Rating
	for rows.Next() {
		var r Rating
		var uid int
		if err := rows.Scan(&r.ID, &r.ArticleID, &uid, &r.AuthorName, &r.Score, &r.CreatedAt); err != nil {
			return nil, err
		}
		if uid != 0 {
			r.UserID = &uid
		}
		r.Score = math.Round(r.Score*100) / 100
		ratings = append(ratings, r)
	}
	return ratings, rows.Err()
}

// Deprecated: Use ListRatingsCtx instead.
func ListRatings(db *sql.DB, articleID int) ([]Rating, error) {
	return ListRatingsCtx(context.TODO(), db, articleID)
}
