package content

import (
	"database/sql"
	"math"
	"strconv"
)

// Rating represents a user's rating for an article.
type Rating struct {
	ID         int     `json:"id"`
	ArticleID  int     `json:"article_id"`
	UserID     *int    `json:"user_id,omitempty"`
	AuthorName string  `json:"author_name"`
	Score      float64 `json:"score"`
	CreatedAt  string  `json:"created_at"`
}

// VoterKey returns the uniqueness key for a rater: "u:<id>" when logged in
// (bound to the session user, immune to author_name spoofing) or "n:<name>"
// for anonymous. The ratings table enforces UNIQUE(article_id, voter_key) so
// a logged-in user cannot vote twice, and anonymous votes are de-duped by
// display name.
func VoterKey(userID *int, authorName string) string {
	if userID != nil {
		return "u:" + strconv.Itoa(*userID)
	}
	return "n:" + authorName
}

// RatingSummary is the aggregate rating info for an article.
type RatingSummary struct {
	Average      float64 `json:"average_score"`
	TotalVoters  int     `json:"total_voters"`
	UserScore    *float64 `json:"user_score,omitempty"` // current user's score, if rated
}

// GetRatingSummary returns aggregate rating for an article, optionally
// including the current rater's score. voterKey is the VoterKey of the caller
// (logged-in "u:<id>" or anonymous "n:<name>"); empty skips the user-score lookup.
func GetRatingSummary(db *sql.DB, articleID int, voterKey string) (*RatingSummary, error) {
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
	if voterKey != "" {
		var score sql.NullFloat64
		err := db.QueryRow(
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

// SetRating upserts a rating (score clamped to [-1, 1]). userID is non-nil for
// logged-in raters (then authorName is the session user's display name and the
// vote is keyed on the user id, preventing spoofing).
func SetRating(db *sql.DB, articleID int, userID *int, authorName string, score float64) (*RatingSummary, error) {
	if score < -1 {
		score = -1
	}
	if score > 1 {
		score = 1
	}
	score = math.Round(score*100) / 100 // 2 decimal places
	voter := VoterKey(userID, authorName)

	_, err := db.Exec(
		`INSERT INTO ratings (article_id, user_id, author_name, voter_key, score)
		 VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE score = VALUES(score), updated_at = CURRENT_TIMESTAMP`,
		articleID, userID, authorName, voter, score,
	)
	if err != nil {
		return nil, err
	}
	return GetRatingSummary(db, articleID, voter)
}

// DeleteRating removes a rater's rating for an article, keyed by voter_key.
func DeleteRating(db *sql.DB, articleID int, voterKey string) (bool, error) {
	res, err := db.Exec(
		`DELETE FROM ratings WHERE article_id = ? AND voter_key = ?`,
		articleID, voterKey,
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
