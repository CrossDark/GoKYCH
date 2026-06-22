"use client";

import { useState, useEffect } from "react";
import { setRating } from "@/lib/api";

interface Props {
  articleType: string;
  articleSlug: string;
  csrfToken: string;
  initialAvg: number;
  initialVoters: number;
  initialUserScore: number | null;
}

export function RatingWidget({
  articleType,
  articleSlug,
  csrfToken,
  initialAvg,
  initialVoters,
  initialUserScore,
}: Props) {
  const [avg, setAvg] = useState(initialAvg);
  const [voters, setVoters] = useState(initialVoters);
  const [userScore, setUserScore] = useState<number | null>(initialUserScore);
  const [loading, setLoading] = useState(false);

  const handleRate = async (score: number) => {
    setLoading(true);
    try {
      const rs = await setRating(articleType, articleSlug, csrfToken, score);
      setAvg(rs.average_score);
      setVoters(rs.total_voters);
      setUserScore(rs.user_score ?? null);
    } catch {
      // silently fail
    } finally {
      setLoading(false);
    }
  };

  const avgPct = Math.round(((avg + 1) / 2) * 100);

  return (
    <div className="rating-widget">
      <div className="rating-bar-track">
        <div
          className="rating-bar-fill"
          style={{ width: `${avgPct}%` }}
        />
      </div>
      <div className="rating-info">
        <span className="rating-avg">
          {avg > 0 ? "+" : ""}{avg.toFixed(2)}
        </span>
        <span className="rating-voters">({voters} 人评分)</span>
      </div>
      <div className="rating-buttons">
        {[-1, 0, 1].map((score) => (
          <button
            key={score}
            className={`rating-btn ${userScore === score ? "active" : ""}`}
            disabled={loading}
            onClick={() => handleRate(score)}
            title={score === 1 ? "赞" : score === -1 ? "踩" : "中立"}
          >
            {score === 1 ? "👍" : score === -1 ? "👎" : "—"}
          </button>
        ))}
      </div>
    </div>
  );
}
