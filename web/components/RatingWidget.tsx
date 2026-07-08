"use client";

import { useState, useEffect, useCallback, useRef } from "react";
import Link from "next/link";
import { setRating, getRating, undoRating, getRatingDetails } from "@/lib/api";
import { useApp } from "./AppProviders";

interface Props {
  articleType: string;
  articleSlug: string;
  initialAvg: number;
  initialVoters: number;
  initialUserScore: number | null;
}

interface RatingDetail {
  id: number;
  article_id: number;
  author_name: string;
  score: number;
  created_at: string;
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
  const [userScore, setUserScore] = useState<number | null>(initialUserScore ?? null);
  const [sliderVal, setSliderVal] = useState(initialUserScore ?? 0);
  const [submitting, setSubmitting] = useState(false);
  const [message, setMessage] = useState("");
  const [messageType, setMessageType] = useState<"" | "error" | "success">("");
  // Auth (user + csrfToken) comes from <AppProviders> context — no
  // per-component getMe/getCsrf fetch.
  const { user, csrfToken } = useApp();
  const isLoggedIn = !!user;
  const [detailOpen, setDetailOpen] = useState(false);
  const [details, setDetails] = useState<RatingDetail[]>([]);
  const [detailsLoading, setDetailsLoading] = useState(false);

  // Client-side fallback for user_score. The article detail endpoint returns
  // user_score from the server-side session context (CurrentUserFromContext),
  // which depends on the SSR fetch successfully forwarding cookies. In the
  // cross-origin production layout (eo.kych.net SSR → api.kych.net), the
  // session cookie must be scoped to a parent Domain (e.g. ".kych.net",
  // via SESSION_COOKIE_DOMAIN) for eo.kych.net's `cookies()` to see it.
  // Without that, the forwarded Cookie header is empty and the backend
  // returns user_score=null even for logged-in voters. The browser-side
  // fetch goes direct with credentials:include so it always sees the
  // cookie — use that here to patch in the real user_score after the
  // client proves we're logged in via getMe(). Skip when SSR already
  // populated it (no-op), or while a rating submit is in flight (avoid
  // clobbering the server-confirmed score).
  useEffect(() => {
    if (!isLoggedIn) return;
    if (userScore != null) return;
    if (submitting) return;
    let cancelled = false;
    (async () => {
      try {
        const rs = await getRating(articleType, articleSlug);
        if (cancelled) return;
        if (rs.user_score != null) {
          setUserScore(rs.user_score);
          setSliderVal(rs.user_score);
        }
      } catch {
        // network blip — leave SSR value (null → 0) in place; user can retry
        // by reloading.
      }
    })();
    return () => { cancelled = true; };
     
  }, [isLoggedIn, articleType, articleSlug, userScore, submitting]);

  // Sync props changes
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAvg(initialAvg);
     
    setVoters(initialVoters);
     
    setUserScore(initialUserScore);
     
    setSliderVal(initialUserScore ?? 0);
  }, [initialAvg, initialVoters, initialUserScore]);

  const msgTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Cleanup timer on unmount
  useEffect(() => {
    return () => {
      if (msgTimerRef.current) clearTimeout(msgTimerRef.current);
    };
  }, []);

  const showMsg = useCallback((msg: string, type: "" | "error" | "success" = "") => {
    setMessage(msg);
    setMessageType(type);
    if (msgTimerRef.current) clearTimeout(msgTimerRef.current);
    msgTimerRef.current = setTimeout(() => { setMessage(""); setMessageType(""); }, 3000);
  }, []);

  const handleSliderChange = async () => {
    if (!isLoggedIn || !csrfToken) {
      if (!isLoggedIn) showMsg("请先登录后再评分", "error");
      setSliderVal(userScore ?? 0);
      return;
    }

    const score = Math.round(sliderVal * 10) / 10; // round to 1 decimal
    if (score === (userScore ?? undefined)) return;

    setSubmitting(true);
    setMessage("提交中...");
    setMessageType("");

    try {
      const rs = await setRating(articleType, articleSlug, csrfToken, score);
      setAvg(rs.average_score);
      setVoters(rs.total_voters);
      setUserScore(rs.user_score ?? null);
      setSliderVal(rs.user_score ?? 0);
      showMsg("评分成功", "success");
    } catch (err: any) {
      showMsg(err.message || "评分失败", "error");
      setSliderVal(userScore ?? 0);
    } finally {
      setSubmitting(false);
    }
  };

  const handleUndo = async () => {
    if (!isLoggedIn || !csrfToken) return;
    setSubmitting(true);
    try {
      const rs = await undoRating(articleType, articleSlug, csrfToken);
      if (rs && rs.average_score !== undefined) {
        setAvg(rs.average_score);
        setVoters(rs.total_voters);
      }
      setUserScore(null);
      setSliderVal(0);
      showMsg("已撤销评分", "success");
    } catch (err: any) {
      showMsg(err.message || "撤销失败", "error");
    } finally {
      setSubmitting(false);
    }
  };

  const loadDetails = async () => {
    setDetailsLoading(true);
    setDetails([]);
    try {
      const data = await getRatingDetails(articleType, articleSlug);
      setDetails(data.ratings ?? []);
    } catch (err: any) {
      console.error("加载评分详情失败:", err);
      setDetails([]);
    } finally {
      setDetailsLoading(false);
    }
  };

  const openDetails = () => {
    setDetailOpen(true);
    loadDetails();
  };

  // Close on ESC
  useEffect(() => {
    if (!detailOpen) return;
    const h = (e: KeyboardEvent) => { if (e.key === "Escape") setDetailOpen(false); };
    document.addEventListener("keydown", h);
    return () => document.removeEventListener("keydown", h);
  }, [detailOpen]);

  const scoreColor = (s: number) => s > 0 ? "#10b981" : s < 0 ? "#ef4444" : "var(--text-muted)";
  const scoreSign = (s: number) => s > 0 ? "+" : "";

  return (
    <section className="rating-section">
      {/* First row: slider + value */}
      <div className="rating-slider-row">
        <span className="rating-slider-label">⭐ 评分</span>
        <div className="rating-slider-container">
          <span className="rating-slider-end">-1</span>
          <input
            type="range"
            className="rating-slider"
            min="-1"
            max="1"
            step="0.1"
            value={sliderVal}
            disabled={!isLoggedIn || submitting}
            onChange={(e) => setSliderVal(parseFloat(e.target.value))}
            onMouseUp={handleSliderChange}
            onTouchEnd={handleSliderChange}
            onKeyUp={(e) => { if (e.key === "ArrowLeft" || e.key === "ArrowRight") handleSliderChange(); }}
          />
          <span className="rating-slider-end">+1</span>
          <span className={`rating-current-value ${sliderVal > 0 ? "positive" : sliderVal < 0 ? "negative" : ""}`}>
            {sliderVal.toFixed(1)}
          </span>
        </div>
      </div>

      {/* Second row: user score + avg + detail button */}
      <div className="rating-info-row">
        {/* Your rating */}
        <div className="rating-user-score">
          <span className="rating-label">你的评分：</span>
          <span className="rating-user-value" style={{ color: userScore != null ? scoreColor(userScore) : "var(--text-muted)" }}>
            {userScore != null ? `${scoreSign(userScore)}${userScore.toFixed(1)}` : "--"}
          </span>
          {userScore != null && isLoggedIn && (
            <button className="rating-undo-btn" onClick={handleUndo} disabled={submitting} title="撤销评分">
              ✕ 撤销
            </button>
          )}
        </div>

        {/* Final score */}
        <div className="rating-final-score">
          <span className="rating-label">最终评分：</span>
          <span className="rating-avg-value" style={{ color: scoreColor(avg) }}>
            {avg > 0 ? "+" : ""}{avg.toFixed(1)}
          </span>
          <span className="rating-voters">（{voters} 人评分）</span>
        </div>

        {/* Detail button */}
        <button className="rating-detail-btn" onClick={openDetails}>
          📋 详细评分
        </button>
      </div>

      {/* Login prompt */}
      {!isLoggedIn && (
        <div className="rating-login-row">
          <Link href={`/auth/login?next=${encodeURIComponent(`/${articleType}/${articleSlug}`)}`}>
            登录
          </Link>
          <span>后可评分</span>
        </div>
      )}

      {/* Message */}
      {message && (
        <div className={`rating-message ${messageType}`}>{message}</div>
      )}

      {/* Detail Modal */}
      {detailOpen && (
        <div className="rating-detail-overlay" onClick={(e) => { if (e.target === e.currentTarget) setDetailOpen(false); }}>
          <div className="rating-detail-modal">
            <div className="rating-detail-modal-header">
              <h3>📋 详细评分记录</h3>
              <button className="rating-detail-close" onClick={() => setDetailOpen(false)}>×</button>
            </div>
            <div className="rating-detail-modal-body">
              {detailsLoading ? (
                <div className="rating-detail-loading">加载中...</div>
              ) : details.length === 0 ? (
                <div className="rating-detail-empty">暂无评分记录</div>
              ) : (
                <table className="rating-detail-table">
                  <thead>
                    <tr>
                      <th>用户</th>
                      <th>评分</th>
                      <th>时间</th>
                    </tr>
                  </thead>
                  <tbody>
                    {details.map((r) => (
                      <tr key={r.id}>
                        <td className="rating-detail-author">{r.author_name}</td>
                        <td className="rating-detail-score" style={{ color: scoreColor(r.score), fontWeight: 700 }}>
                          {scoreSign(r.score)}{r.score.toFixed(1)}
                        </td>
                        <td className="rating-detail-time">
                          {r.created_at ? r.created_at.substring(0, 16).replace("T", " ") : ""}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
