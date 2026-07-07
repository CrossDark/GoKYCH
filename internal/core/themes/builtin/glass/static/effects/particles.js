/* Glass v2 — particles.js
 * ────────────────────────────────────────────────────────────
 * 透明玻璃主题的 Canvas 粒子效果。客户端纯 JS, 零服务器开销。
 *
 * 三种模式:
 *   - none      (默认)  不创建 Canvas, 完全零开销
 *   - rain              雨滴下落, 200~400 粒子
 *   - sunlight          阳光光斑缓慢漂移, 1~2 个
 *
 * 性能约束:
 *   - 离屏画布一次性生成 sprite, 运行时只 blit (drawImage)
 *   - 全部走 transform/位移 + alpha composite, 不开 filter
 *   - 自适应密度: 60 帧 FPS < 45 时砍半粒子
 *   - prefers-reduced-motion / 低端机(<4GB / <4核) / 移动端: 自动 none
 *   - document.hidden 时 cancelAnimationFrame, 切回 tab 自动恢复
 *   - 移动端默认不开, 避免发热
 *
 * 配置 (localStorage):
 *   gokych:glass:effect_mode       "none" | "rain" | "sunlight"
 *   gokych:glass:particle_density  "0".."100"
 *   gokych:glass:background_image  URL 字符串(直接套到 CSS 变量)
 *
 * 公开 API:
 *   window.GlassFX.setMode(mode)
 *   window.GlassFX.getMode()
 *   window.GlassFX.setDensity(0..100)
 *   window.GlassFX.setBackgroundImage(url)  // url 为空字符串清掉
 */
(function () {
    'use strict';

    // ── 常量 ────────────────────────────────────────────
    const STORAGE_KEYS = {
        mode: 'gokych:glass:effect_mode',
        density: 'gokych:glass:particle_density',
        bgImage: 'gokych:glass:background_image',
    };
    const DEFAULT_DENSITY = 60;
    const MAX_PARTICLES = 400;
    const MIN_PARTICLES = 30;
    const FPS_LOW_THRESHOLD = 45; // 平均 FPS 低于此值砍半
    const FPS_SAMPLE_FRAMES = 60; // 采样窗口

    // ── 能力检测 ────────────────────────────────────────
    function shouldEnable() {
        try {
            if (window.matchMedia &&
                window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
                return false;
            }
            const dm = navigator.deviceMemory;
            if (typeof dm === 'number' && dm < 4) return false;
            const hc = navigator.hardwareConcurrency;
            if (typeof hc === 'number' && hc < 4) return false;
            if (/Mobi|Android|iPhone|iPad|iPod/i.test(navigator.userAgent)) return false;
        } catch (e) { /* 任何检测失败都放行 */ }
        return true;
    }

    // ── Sprite 生成 (一次性, 离屏) ─────────────────────
    // 雨滴: 上尖下圆的椭圆, 浅蓝半透, 垂直渐变
    function makeRainSprite(size) {
        const c = document.createElement('canvas');
        c.width = Math.ceil(size * 0.6);  // 雨滴细长, 节省显存
        c.height = size;
        const ctx = c.getContext('2d');
        const w = c.width, h = c.height;
        const grad = ctx.createLinearGradient(0, 0, 0, h);
        grad.addColorStop(0, 'rgba(200, 230, 255, 0.0)');
        grad.addColorStop(0.35, 'rgba(180, 220, 255, 0.55)');
        grad.addColorStop(1, 'rgba(160, 200, 255, 0.92)');
        ctx.fillStyle = grad;
        ctx.beginPath();
        ctx.moveTo(w / 2, 0);
        ctx.bezierCurveTo(w * 0.95, h * 0.25, w * 0.7, h * 0.85, w / 2, h);
        ctx.bezierCurveTo(w * 0.3, h * 0.85, w * 0.05, h * 0.25, w / 2, 0);
        ctx.fill();
        return c;
    }

    // 阳光光斑: 中心明亮, 边缘渐隐, 暖色
    function makeSunSprite(size) {
        const c = document.createElement('canvas');
        c.width = c.height = size;
        const ctx = c.getContext('2d');
        const cx = size / 2, cy = size / 2;
        const grad = ctx.createRadialGradient(cx, cy, 0, cx, cy, size / 2);
        grad.addColorStop(0, 'rgba(255, 250, 220, 0.65)');
        grad.addColorStop(0.25, 'rgba(255, 230, 170, 0.42)');
        grad.addColorStop(0.55, 'rgba(255, 210, 140, 0.18)');
        grad.addColorStop(0.85, 'rgba(255, 200, 110, 0.04)');
        grad.addColorStop(1, 'rgba(255, 200, 110, 0)');
        ctx.fillStyle = grad;
        ctx.fillRect(0, 0, size, size);
        return c;
    }

    // ── 主类 ──────────────────────────────────────────
    class GlassFX {
        constructor() {
            this._layer = null;
            this._canvas = null;
            this._ctx = null;
            this._mode = 'none';
            this._density = DEFAULT_DENSITY;
            this._particles = [];
            this._sprites = { rain: null, sun: null };
            this._rafId = null;
            this._lastTs = 0;
            this._fpsSamples = [];
            this._w = 0;
            this._h = 0;
            this._enabled = shouldEnable();
            this._resizeObs = null;
            this._visibilityHandler = null;
            this._init();
        }

        _init() {
            if (!this._enabled) {
                // 仍然监听 backgroundImage 设置, 那一部分零开销
                this._loadConfig().finally(() => this._applyStoredBgImage());
                return;
            }
            this._createLayer();
            this._visibilityHandler = () => {
                if (document.hidden) this._pause();
                else this._resume();
            };
            document.addEventListener('visibilitychange', this._visibilityHandler);
            // Config load is async — first we render the fx layer empty
            // (none mode), then once the server config arrives we switch
            // to the admin's choice. The two-step avoids a flash of rain
            // when the saved mode is none.
            this._loadConfig().then(() => {
                if (this._mode !== 'none') this.setMode(this._mode);
            });
        }

        // _loadConfig fetches the EFFECTIVE settings for glass from
        // /api/themes/glass/settings (schema from theme.yaml + admin
        // overrides from theme_settings table). Falls back to
        // localStorage if the request fails, and to hardcoded sane
        // defaults if neither is set. Setting backgroundImage does NOT
        // require this — it has its own localStorage key for the
        // per-user override and a CSS variable that the admin value
        // overrides when the user visits admin.
        async _loadConfig() {
            try {
                const r = await fetch('/api/themes/glass/settings', { cache: 'no-store' });
                if (!r.ok) throw new Error('settings HTTP ' + r.status);
                const data = await r.json();
                const schema = (data && data.schema) || [];
                const values = (data && data.values) || {};
                const def = (k) => {
                    const e = schema.find((s) => s.key === k);
                    return e ? e.default : undefined;
                };
                // mode
                const modeVal = values.effect_mode != null ? values.effect_mode : def('effect_mode');
                if (modeVal === 'rain' || modeVal === 'sunlight' || modeVal === 'none') {
                    this._mode = modeVal;
                }
                // density
                const densityVal = values.particle_density != null
                    ? parseInt(values.particle_density, 10)
                    : (typeof def('particle_density') === 'number' ? def('particle_density') : NaN);
                if (!isNaN(densityVal)) this._density = Math.max(0, Math.min(100, densityVal));
                // background_image — server WINS. We write the CSS
                // variable so body { background-image: var(--glass-bg-image), ... }
                // picks it up. This is what was missing before: the
                // admin's background_image value was being persisted to
                // theme_settings but never applied to --glass-bg-image,
                // so the admin's upload looked like a no-op.
                const bgVal = values.background_image != null
                    ? String(values.background_image)
                    : (def('background_image') != null ? String(def('background_image')) : '');
                if (bgVal && bgVal.length > 0) {
                    this.setBackgroundImage(bgVal);
                } else {
                    // admin cleared their background — fall back to any
                    // per-visitor localStorage pick (offline / dev only)
                    this._applyStoredBgImage();
                }
                return;
            } catch (e) {
                // network/CORS/etc — fall back to localStorage entirely
                this._applyStoredBgImage();
            }
            // localStorage fallback
            const m = localStorage.getItem(STORAGE_KEYS.mode);
            if (m === 'rain' || m === 'sunlight' || m === 'none') this._mode = m;
            const d = parseInt(localStorage.getItem(STORAGE_KEYS.density) || '', 10);
            if (!isNaN(d)) this._density = Math.max(0, Math.min(100, d));
        }

        _createLayer() {
            let layer = document.getElementById('glass-fx-layer');
            if (!layer) {
                layer = document.createElement('div');
                layer.id = 'glass-fx-layer';
                layer.setAttribute('aria-hidden', 'true');
                document.body.appendChild(layer);
            }
            this._layer = layer;
            // canvas
            const canvas = document.createElement('canvas');
            canvas.setAttribute('aria-hidden', 'true');
            this._layer.appendChild(canvas);
            this._canvas = canvas;
            this._ctx = canvas.getContext('2d', { alpha: true });
            this._resize();
            if (typeof ResizeObserver !== 'undefined') {
                this._resizeObs = new ResizeObserver(() => this._resize());
                this._resizeObs.observe(document.documentElement);
            } else {
                window.addEventListener('resize', () => this._resize());
            }
        }

        _resize() {
            if (!this._canvas) return;
            const dpr = Math.min(window.devicePixelRatio || 1, 2);
            const w = window.innerWidth;
            const h = window.innerHeight;
            this._canvas.width = Math.floor(w * dpr);
            this._canvas.height = Math.floor(h * dpr);
            this._canvas.style.width = w + 'px';
            this._canvas.style.height = h + 'px';
            // setTransform 覆盖之前的 scale, 避免连续 _resize 累积
            this._ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
            this._w = w;
            this._h = h;
        }

        // ── 公开 API ───────────────────────────────────
        getMode() { return this._mode; }

        setMode(mode) {
            if (!this._enabled && mode !== 'none') {
                console.info('[glass-fx] disabled on this device; mode not changed');
                return;
            }
            this._dispose();
            this._mode = mode;
            localStorage.setItem(STORAGE_KEYS.mode, mode);
            if (mode === 'rain') this._setupRain();
            else if (mode === 'sunlight') this._setupSunlight();
            // 'none' 不启动 rAF, 完全零开销
        }

        setDensity(d) {
            this._density = Math.max(0, Math.min(100, d | 0));
            localStorage.setItem(STORAGE_KEYS.density, String(this._density));
            if (this._mode === 'rain') this._setupRain();
            else if (this._mode === 'sunlight') this._setupSunlight();
        }

        setBackgroundImage(url) {
            if (url) {
                document.documentElement.style.setProperty('--glass-bg-image', `url("${url}")`);
                localStorage.setItem(STORAGE_KEYS.bgImage, url);
            } else {
                document.documentElement.style.removeProperty('--glass-bg-image');
                localStorage.removeItem(STORAGE_KEYS.bgImage);
            }
        }

        _applyStoredBgImage() {
            const url = localStorage.getItem(STORAGE_KEYS.bgImage);
            if (url) this.setBackgroundImage(url);
        }

        // ── 雨滴 ───────────────────────────────────────
        _setupRain() {
            const n = Math.max(MIN_PARTICLES, Math.floor(this._density / 100 * MAX_PARTICLES));
            if (!this._sprites.rain) this._sprites.rain = makeRainSprite(20);
            this._particles = [];
            for (let i = 0; i < n; i++) this._particles.push(this._makeRainDrop());
            this._lastTs = 0;
            this._fpsSamples = [];
            this._schedule();
        }

        _makeRainDrop() {
            return {
                x: Math.random() * this._w,
                y: Math.random() * this._h,
                speed: 220 + Math.random() * 280,
                drift: (Math.random() - 0.5) * 35,
                // slideDrift is the per-particle extra horizontal push
                // applied once it hits the glass surface — gives the
                // "droplets race down the window" feel. Signed so half
                // slide left, half right.
                slideDrift: 50 + Math.random() * 60,
                size: 0.55 + Math.random() * 0.8,
                alpha: 0.25 + Math.random() * 0.55,
                // 'falling' → vertical raindrop at full alpha.
                // 'sliding' → once y crosses 70% of canvas height, the
                // drop "hits" the transparent glass and runs sideways
                // while alpha fades. When alpha drops below 0.05 the
                // drop is respawned at the top — no puddling at the
                // bottom, just droplets streaking off the page.
                phase: 'falling',
            };
        }

        _drawRain(dt) {
            const sprite = this._sprites.rain;
            const sw = sprite.width, sh = sprite.height;
            // The "glass surface" line — once a drop's y crosses this
            // it switches to sliding. 0.72 of canvas height (a bit
            // before the very bottom) gives a visible ~28% strip where
            // you can watch drops run down and fade.
            const slideLine = this._h * 0.72;
            const slideSpan = this._h - slideLine;
            for (let i = 0; i < this._particles.length; i++) {
                const p = this._particles[i];
                if (p.phase === 'falling') {
                    p.y += p.speed * dt;
                    p.x += p.drift * dt;
                    if (p.y >= slideLine) {
                        p.phase = 'sliding';
                        // Pin y to the slide line so the drop settles
                        // against the glass surface visually.
                        p.y = slideLine + Math.random() * 4;
                    }
                } else {
                    // sliding: nearly no vertical motion, big
                    // horizontal drift, alpha fades to 0 over the
                    // remaining height. Direction of slideDrift is
                    // baked in at spawn time (positive or zero → slide
                    // right, negative → left) so adjacent drops streak
                    // in different directions and the page doesn't
                    // look like a one-way conveyor.
                    p.x += p.slideDrift * dt;
                    p.y += p.speed * 0.04 * dt;
                    const progress = Math.min(1, Math.max(0, (p.y - slideLine) / Math.max(1, slideSpan)));
                    p.alpha = Math.max(0, 0.55 * (1 - progress));
                }
                // Wrap horizontally during falling phase so drops
                // near the edge don't disappear prematurely.
                if (p.phase === 'falling') {
                    if (p.x < -sw) p.x = this._w + sw;
                    if (p.x > this._w + sw) p.x = -sw;
                }
                // Respawn when faded out (sliding) or off-screen
                // (falling). Each drop has its own lifetime — at peak
                // density the bottom 28% of the page is a continuous
                // stream of fading streaks, not a static pool.
                if (p.alpha <= 0.05 || p.y > this._h + sh) {
                    p.x = Math.random() * this._w;
                    p.y = -sh;
                    p.speed = 220 + Math.random() * 280;
                    p.drift = (Math.random() - 0.5) * 35;
                    p.slideDrift = 50 + Math.random() * 60;
                    p.size = 0.55 + Math.random() * 0.8;
                    p.alpha = 0.25 + Math.random() * 0.55;
                    p.phase = 'falling';
                    continue;
                }
                // Render. For sliding drops, rotate ~50° so the
                // vertical sprite reads as a streak running sideways
                // along the glass. The rotation is local to each
                // drawImage via save/translate/rotate/restore.
                if (p.phase === 'sliding') {
                    this._ctx.save();
                    this._ctx.translate(p.x, p.y);
                    this._ctx.rotate(Math.PI * 0.28); // ~50° tilt
                    this._ctx.globalAlpha = p.alpha;
                    this._ctx.drawImage(sprite,
                        -sw * p.size / 2,
                        -sh * p.size / 2,
                        sw * p.size, sh * p.size);
                    this._ctx.restore();
                } else {
                    this._ctx.globalAlpha = p.alpha;
                    this._ctx.drawImage(sprite,
                        p.x - sw * p.size / 2,
                        p.y - sh * p.size / 2,
                        sw * p.size, sh * p.size);
                }
            }
            this._ctx.globalAlpha = 1;
        }

        // ── 阳光光斑 ───────────────────────────────────
        _setupSunlight() {
            const n = this._density > 50 ? 2 : 1;
            if (!this._sprites.sun) this._sprites.sun = makeSunSprite(420);
            this._particles = [];
            for (let i = 0; i < n; i++) {
                this._particles.push({
                    x: Math.random() * this._w,
                    y: Math.random() * this._h,
                    tx: Math.random() * this._w,
                    ty: Math.random() * this._h,
                    speed: 0.0002 + Math.random() * 0.0003,
                    size: 0.55 + Math.random() * 0.5,
                });
            }
            this._lastTs = 0;
            this._fpsSamples = [];
            this._schedule();
        }

        _drawSun(dt) {
            const sprite = this._sprites.sun;
            const baseSize = sprite.width;
            for (let i = 0; i < this._particles.length; i++) {
                const p = this._particles[i];
                const dx = p.tx - p.x, dy = p.ty - p.y;
                if (Math.hypot(dx, dy) < 8) {
                    p.tx = Math.random() * this._w;
                    p.ty = Math.random() * this._h;
                } else {
                    p.x += dx * p.speed * dt * 60;
                    p.y += dy * p.speed * dt * 60;
                }
                this._ctx.globalAlpha = 0.5;
                const sz = baseSize * p.size;
                this._ctx.drawImage(sprite, p.x - sz / 2, p.y - sz / 2, sz, sz);
            }
            this._ctx.globalAlpha = 1;
        }

        // ── 主循环 ─────────────────────────────────────
        _schedule() {
            if (this._rafId) return;
            this._rafId = requestAnimationFrame((t) => this._tick(t));
        }

        _tick(ts) {
            this._rafId = null;
            if (this._mode === 'none' || document.hidden) return;
            const dt = this._lastTs ? Math.min(0.05, (ts - this._lastTs) / 1000) : 0.016;
            this._lastTs = ts;
            // FPS 采样
            if (dt > 0) {
                this._fpsSamples.push(1 / dt);
                if (this._fpsSamples.length > FPS_SAMPLE_FRAMES) this._fpsSamples.shift();
                if (this._fpsSamples.length === FPS_SAMPLE_FRAMES) {
                    let sum = 0;
                    for (let i = 0; i < FPS_SAMPLE_FRAMES; i++) sum += this._fpsSamples[i];
                    const avg = sum / FPS_SAMPLE_FRAMES;
                    if (avg < FPS_LOW_THRESHOLD && this._particles.length > MIN_PARTICLES) {
                        const next = Math.max(MIN_PARTICLES, Math.floor(this._particles.length / 2));
                        this._particles.length = next;
                        this._fpsSamples = [];
                        console.info('[glass-fx] FPS low, halving particles to', next);
                    }
                }
            }
            this._ctx.clearRect(0, 0, this._w, this._h);
            if (this._mode === 'rain') this._drawRain(dt);
            else if (this._mode === 'sunlight') this._drawSun(dt);
            this._schedule();
        }

        _dispose() {
            if (this._rafId) {
                cancelAnimationFrame(this._rafId);
                this._rafId = null;
            }
            this._particles = [];
            if (this._ctx && this._w) this._ctx.clearRect(0, 0, this._w, this._h);
        }

        _pause() {
            if (this._rafId) {
                cancelAnimationFrame(this._rafId);
                this._rafId = null;
            }
        }

        _resume() {
            if (this._mode !== 'none' && !this._rafId) {
                this._lastTs = 0;
                this._schedule();
            }
        }
    }

    // ── 挂载 ──────────────────────────────────────────
    function mount() {
        if (window.GlassFX) return;
        window.GlassFX = new GlassFX();
    }
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', mount, { once: true });
    } else {
        mount();
    }
})();
