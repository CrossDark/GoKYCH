"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { setRating, getCsrf, getMe } from "@/lib/api";

interface Props {
  articleType: string;
  articleSlug: string;
  initialAvg: number;
  initialVoters: number;
  initialUserScore: number | null;
}

export function RatingWidget({
  articleType,
  articleSlug,
  initialAvg,
  initialVoters,
  initialUserScore,
}: Props) {
  const [avg, setAvg] = useState(initialAvg);
  const [voters, setVoters] = useState(initialVoters);
  const [userScore, setUserScore] = useState<number | null>(initialUserScore);
  const [loading, setLoading] = useState(false);
  const [csrfToken, setCsrfToken] = useState("");
  const [isLoggedIn, setIsLoggedIn] = useState(true); // optimistic: show buttons until proven otherwise

  useEffect(() => {
    getMe().then((r) => {
      if (r.user) {
        setIsLoggedIn(true);
        getCsrf().then((c) => setCsrfToken(c.csrf_token)).catch(() => {});
      } else {
        setIsLoggedIn(false);
      }
    }).catch(() => {
      setIsLoggedIn(false);
    });
  }, []);

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
        {isLoggedIn ? (
          [-1, 0, 1].map((score) => (
            <button
              key={score}
              className={`rating-btn ${userScore === score ? "active" : ""}`}
              disabled={loading}
              onClick={() => handleRate(score)}
              title={score === 1 ? "赞" : score === -1 ? "踩" : "中立"}
            >
              {score === 1 ? "👍" : score === -1 ? "👎" : "—"}
            </button>
          ))
        ) : (
          <span className="rating-login-prompt">
            <Link href={`/auth/login?next=${encodeURIComponent(`/${articleType}/${articleSlug}`)}`}>
              登录
            </Link>
            后可评分
          </span>
        )}
      </div>
    </div>
  );
}
