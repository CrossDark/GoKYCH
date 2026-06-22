package content

import (
	"database/sql"
	"math"
)

// Rating represents a user's rating for an article.
type Rating struct {
	ID         int     `json:"id"`
	ArticleID  int     `json:"article_id"`
	AuthorName string  `json:"author_name"`
	Score      float64 `json:"score"`
	CreatedAt  string  `json:"created_at"`
}

// RatingSummary is the aggregate rating info for an article.
type RatingSummary struct {
	Average      float64 `json:"average_score"`
	TotalVoters  int     `json:"total_voters"`
	UserScore    *float64 `json:"user_score,omitempty"` // current user's score, if rated
}

// GetRatingSummary returns aggregate rating for an article, optionally
// including the current user's score.
func GetRatingSummary(db *sql.DB, articleID int, userName string) (*RatingSummary, error) {
	rs := &RatingSummary{}
	var avg sql.NullFloat64
	var total int
	err := db.QueryRow(
		`SELECT COUNT(*), AVG(score) FROM ratings WHERE article_id = ?`, articleID,
	).Scan(&total, &avg)
	if err != nil {
		return nil, err
	}
	rs.TotalVoters = total
	if avg.Valid {
		rs.Average = math.Round(avg.Float64*100) / 100
	}
	if userName != "" {
		var score sql.NullFloat64
		err := db.QueryRow(
			`SELECT score FROM ratings WHERE article_id = ? AND author_name = ?`,
			articleID, userName,
		).Scan(&score)
		if err == nil && score.Valid {
			s := math.Round(score.Float64*100) / 100
			rs.UserScore = &s
		}
	}
	return rs, nil
}

// SetRating upserts a rating (score clamped to [-1, 1]).
func SetRating(db *sql.DB, articleID int, authorName string, score float64) (*RatingSummary, error) {
	if score < -1 {
		score = -1
	}
	if score > 1 {
		score = 1
	}
	score = math.Round(score*100) / 100 // 2 decimal places

	_, err := db.Exec(
		`INSERT INTO ratings (article_id, author_name, score)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE score = VALUES(score)`,
		articleID, authorName, score,
	)
	if err != nil {
		return nil, err
	}
	return GetRatingSummary(db, articleID, authorName)
}

// DeleteRating removes a user's rating for an article.
func DeleteRating(db *sql.DB, articleID int, authorName string) (bool, error) {
	res, err := db.Exec(
		`DELETE FROM ratings WHERE article_id = ? AND author_name = ?`,
		articleID, authorName,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListRatings returns all ratings for an article, ordered by most recent.
func ListRatings(db *sql.DB, articleID int) ([]Rating, error) {
	rows, err := db.Query(
		`SELECT id, article_id, author_name, score, COALESCE(created_at, '') FROM ratings
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
		if err := rows.Scan(&r.ID, &r.ArticleID, &r.AuthorName, &r.Score, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Score = math.Round(r.Score*100) / 100
		ratings = append(ratings, r)
	}
	return ratings, rows.Err()
}
