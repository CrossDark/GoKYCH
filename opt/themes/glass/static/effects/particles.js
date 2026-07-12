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
 *   (card_alpha 不入 localStorage — 仅 admin 通过后台设置, 离线场景
 *    退回 theme.css 默认值 0.42 已经足够)
 *
 * 公开 API:
 *   window.GlassFX.setMode(mode)
 *   window.GlassFX.getMode()
 *   window.GlassFX.setDensity(0..100)
 *   window.GlassFX.setBackgroundImage(url)  // url 为空字符串清掉
 *   window.GlassFX.setCardAlpha(0..100)     // 卡片底色透明度, 0..100 整数
 *   window.GlassFX.setCardFrost(0..100)     // 悬浮卡片磨砂 (--glass-card-frost)
 *   window.GlassFX.setIsolationFrost(0..100)// 隔离层磨砂 (--glass-isolation-frost)
 *   window.GlassFX.setSidebarFrost(0..100)  // 左右侧栏磨砂 (--glass-sidebar-frost)
 *   window.GlassFX.reload()                 // 重新拉 server settings, admin 保存后调
 */
(function () {
    'use strict';

    // ── 常量 ────────────────────────────────────────────
    const STORAGE_KEYS = {
        mode: 'gokych:glass:effect_mode',
        density: 'gokych:glass:particle_density',
        bgImage: 'gokych:glass:background_image',
    };
    function apiBase() {
        return (window.__GK_API_BASE_URL || '');
    }
    function apiPath(path) {
        if (!path.startsWith('/')) path = '/' + path;
        return apiBase() + path;
    }
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
    // 雨滴: 更细长的椭圆 (height/width ≈ 3, 之前 1.67), 顶尖锐底略圆,
    // 浅蓝半透, 垂直渐变, 真实雨丝的形状.
    function makeRainSprite(size) {
        const c = document.createElement('canvas');
        c.width = Math.max(3, Math.ceil(size * 0.32));  // 更细: w/h ≈ 0.32
        c.height = size;
        const ctx = c.getContext('2d');
        const w = c.width, h = c.height;
        const grad = ctx.createLinearGradient(0, 0, 0, h);
        grad.addColorStop(0,    'rgba(200, 230, 255, 0.0)');
        grad.addColorStop(0.30, 'rgba(180, 220, 255, 0.45)');
        grad.addColorStop(0.70, 'rgba(160, 205, 255, 0.80)');
        grad.addColorStop(1,    'rgba(140, 190, 255, 0.92)');
        ctx.fillStyle = grad;
        ctx.beginPath();
        // 顶部更尖, 底部略宽
        ctx.moveTo(w / 2, 0);
        ctx.bezierCurveTo(w * 1.05, h * 0.30, w * 0.85, h * 0.90, w / 2, h);
        ctx.bezierCurveTo(w * 0.15, h * 0.90, w * -0.05, h * 0.30, w / 2, 0);
        ctx.fill();
        return c;
    }

    // 阳光光斑 — lens flare 风格, 模拟阳光打在玻璃上散射.
    // 分 4 层叠加: 大光晕 + 中等光晕 + 中心亮 core + 横/竖光柱.
    // 4 层一起渲染出"光散射 + 高光"的双重感, 比单 radial gradient
    // 真实得多.  sprite 画在 600x600 (size 参数), 0~size/2 半径.
    function makeSunSprite(size) {
        const c = document.createElement('canvas');
        c.width = c.height = size;
        const ctx = c.getContext('2d');
        const cx = size / 2, cy = size / 2;

        // 1. 最外层大光晕 — 暖黄, 几乎透明, 覆盖整个 sprite
        let g = ctx.createRadialGradient(cx, cy, 0, cx, cy, size / 2);
        g.addColorStop(0,    'rgba(255, 240, 200, 0.18)');
        g.addColorStop(0.35, 'rgba(255, 220, 160, 0.10)');
        g.addColorStop(0.70, 'rgba(255, 200, 120, 0.04)');
        g.addColorStop(1,    'rgba(255, 200, 120, 0)');
        ctx.fillStyle = g;
        ctx.fillRect(0, 0, size, size);

        // 2. 中等光晕 — 暖色更深, 半径 size/3
        g = ctx.createRadialGradient(cx, cy, 0, cx, cy, size / 3);
        g.addColorStop(0,    'rgba(255, 250, 220, 0.35)');
        g.addColorStop(0.45, 'rgba(255, 235, 180, 0.20)');
        g.addColorStop(0.85, 'rgba(255, 215, 140, 0.05)');
        g.addColorStop(1,    'rgba(255, 215, 140, 0)');
        ctx.fillStyle = g;
        ctx.fillRect(0, 0, size, size);

        // 3. 中心亮 core — 接近白色, 半径 size/12, 实际最亮的"光点"
        g = ctx.createRadialGradient(cx, cy, 0, cx, cy, size / 12);
        g.addColorStop(0,    'rgba(255, 255, 245, 0.90)');
        g.addColorStop(0.4,  'rgba(255, 248, 220, 0.55)');
        g.addColorStop(0.8,  'rgba(255, 230, 170, 0.15)');
        g.addColorStop(1,    'rgba(255, 220, 150, 0)');
        ctx.fillStyle = g;
        ctx.fillRect(0, 0, size, size);

        // 4. 横向 + 纵向光柱 — 玻璃透光散射的标志性效果, 用 'lighter'
        // 合成让多层叠加时更亮, 模拟镜头的 lens flare.
        ctx.save();
        ctx.globalCompositeOperation = 'lighter';
        // 横向光柱 (长亮带)
        let lg = ctx.createLinearGradient(0, cy, size, cy);
        lg.addColorStop(0,    'rgba(255, 250, 220, 0)');
        lg.addColorStop(0.4,  'rgba(255, 250, 220, 0.18)');
        lg.addColorStop(0.5,  'rgba(255, 250, 220, 0.32)');  // 中心最亮
        lg.addColorStop(0.6,  'rgba(255, 250, 220, 0.18)');
        lg.addColorStop(1,    'rgba(255, 250, 220, 0)');
        ctx.fillStyle = lg;
        ctx.fillRect(0, cy - 3, size, 6);
        // 纵向光柱
        lg = ctx.createLinearGradient(cx, 0, cx, size);
        lg.addColorStop(0,    'rgba(255, 250, 220, 0)');
        lg.addColorStop(0.4,  'rgba(255, 250, 220, 0.10)');
        lg.addColorStop(0.5,  'rgba(255, 250, 220, 0.22)');
        lg.addColorStop(0.6,  'rgba(255, 250, 220, 0.10)');
        lg.addColorStop(1,    'rgba(255, 250, 220, 0)');
        ctx.fillStyle = lg;
        ctx.fillRect(cx - 3, 0, 6, size);
        // 短斜光柱 (左下到右上) — 真正的镜头眩光, 长度只有 sprite 的 35%
        ctx.translate(cx, cy);
        ctx.rotate(-Math.PI / 4);  // -45°
        lg = ctx.createLinearGradient(-size * 0.15, 0, size * 0.20, 0);
        lg.addColorStop(0,   'rgba(255, 230, 180, 0)');
        lg.addColorStop(0.5, 'rgba(255, 245, 210, 0.15)');
        lg.addColorStop(1,   'rgba(255, 230, 180, 0)');
        ctx.fillStyle = lg;
        ctx.fillRect(-size * 0.15, -2, size * 0.35, 4);
        ctx.restore();

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
                const r = await fetch(apiPath('/api/themes/glass/settings'), { cache: 'no-store' });
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
                // card_alpha — server value (0..100 整数) 直接套到
                // --glass-card-alpha CSS 变量 (除以 100 转 0..1)。
                // theme.css 把这个变量插进 rgba(), 所以 admin 调一调
                // 玻璃卡片的透明度就即时生效 (hover 自动 +0.13 由
                // CSS calc 处理, 不用 JS 算)。如果没设, theme.css 的
                // 默认 0.42 自己顶上去。
                const alphaVal = values.card_alpha != null
                    ? parseInt(values.card_alpha, 10)
                    : (typeof def('card_alpha') === 'number' ? def('card_alpha') : NaN);
                if (!isNaN(alphaVal)) {
                    const clamped = Math.max(0, Math.min(100, alphaVal));
                    this.setCardAlpha(clamped);
                }
                // 三个磨砂强度滑条 — admin 在后台拖, 写进 theme_settings
                // 表, 这里的 _loadConfig 读出来 setProperty 注入 CSS 变量。
                // 跟 card_alpha 一样不写 localStorage, 离线退化到 theme.css
                // 默认值 (24 / 12 / 28px)。三层 fallback: server override →
                // schema default → 不调 setter, theme.css 默认值生效。
                const cardFrostVal = values.card_frost != null
                    ? parseInt(values.card_frost, 10)
                    : (typeof def('card_frost') === 'number' ? def('card_frost') : NaN);
                if (!isNaN(cardFrostVal)) this.setCardFrost(cardFrostVal);
                const isolationFrostVal = values.isolation_frost != null
                    ? parseInt(values.isolation_frost, 10)
                    : (typeof def('isolation_frost') === 'number' ? def('isolation_frost') : NaN);
                if (!isNaN(isolationFrostVal)) this.setIsolationFrost(isolationFrostVal);
                const sidebarFrostVal = values.sidebar_frost != null
                    ? parseInt(values.sidebar_frost, 10)
                    : (typeof def('sidebar_frost') === 'number' ? def('sidebar_frost') : NaN);
                if (!isNaN(sidebarFrostVal)) this.setSidebarFrost(sidebarFrostVal);
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

        // setCardAlpha(value 0..100) — 把卡片透明度直接套到 CSS 变量。
        // 不写 localStorage: 该值由 admin 在后台配, 离线场景退化到
        // theme.css 默认值即可, 不需要每个浏览器本地存一份。
        setCardAlpha(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            document.documentElement.style.setProperty('--glass-card-alpha', String(v / 100));
        }

        // setCardFrost / setIsolationFrost / setSidebarFrost (value 0..100) —
        // 三个磨砂强度滑条的控制。schema 0-100 整数, 这里 0.4 倍率映射到
        // 0-40px 直接套到 --glass-*-frost CSS 变量 (theme.css 引用)。
        // 0-100 范围设计是为了跟 card_alpha 范围一致 (admin UI 一致性);
        // 40px 上限对 3 个目标都够 (卡片 default 24, 隔离 default 12, 侧栏
        // default 28)。同样不写 localStorage: 离线退化到 theme.css 默认值。
        setCardFrost(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            document.documentElement.style.setProperty('--glass-card-frost', (v * 0.4).toFixed(1) + 'px');
        }
        setIsolationFrost(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            document.documentElement.style.setProperty('--glass-isolation-frost', (v * 0.4).toFixed(1) + 'px');
        }
        setSidebarFrost(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            document.documentElement.style.setProperty('--glass-sidebar-frost', (v * 0.4).toFixed(1) + 'px');
        }

        // reload() — 重新拉 server settings, 把 admin 后台最新保存的
        // card_frost / isolation_frost / sidebar_frost / effect_mode 等
        // 重新 setProperty 到 CSS 变量。 admin ThemeSettingsModal 保存后
        // 会调 window.GlassFX?.reload?.() (在客户端, 跟 particles.js 同源)
        // 让滑条调整立即反映在当前页面。 返回 Promise<boolean> 表示
        // 是否真的重新拉了 (network 失败 false, 但不抛错)。
        async reload() {
            try {
                const r = await fetch(apiPath('/api/themes/glass/settings'), { cache: 'no-store' });
                if (!r.ok) return false;
                const data = await r.json();
                const schema = (data && data.schema) || [];
                const values = (data && data.values) || {};
                const def = (k) => {
                    const e = schema.find((s) => s.key === k);
                    return e ? e.default : undefined;
                };
                // mode — 跟 _loadConfig 一样, 但要切换实际模式
                const modeVal = values.effect_mode != null ? values.effect_mode : def('effect_mode');
                if (modeVal === 'rain' || modeVal === 'sunlight' || modeVal === 'none') {
                    this._mode = modeVal;
                    this.setMode(modeVal);
                }
                // density
                const densityVal = values.particle_density != null
                    ? parseInt(values.particle_density, 10)
                    : (typeof def('particle_density') === 'number' ? def('particle_density') : NaN);
                if (!isNaN(densityVal)) this.setDensity(densityVal);
                // card_alpha
                const alphaVal = values.card_alpha != null
                    ? parseInt(values.card_alpha, 10)
                    : (typeof def('card_alpha') === 'number' ? def('card_alpha') : NaN);
                if (!isNaN(alphaVal)) this.setCardAlpha(alphaVal);
                // 三个磨砂强度
                for (const [key, setter] of [
                    ['card_frost', (v) => this.setCardFrost(v)],
                    ['isolation_frost', (v) => this.setIsolationFrost(v)],
                    ['sidebar_frost', (v) => this.setSidebarFrost(v)],
                ]) {
                    const v = values[key] != null
                        ? parseInt(values[key], 10)
                        : (typeof def(key) === 'number' ? def(key) : NaN);
                    if (!isNaN(v)) setter(v);
                }
                // background_image
                const bgVal = values.background_image != null
                    ? String(values.background_image)
                    : (def('background_image') != null ? String(def('background_image')) : '');
                this.setBackgroundImage(bgVal);
                return true;
            } catch (e) {
                return false;
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
            // Wind: a small global "breeze" that all drops share. Random
            // sign and magnitude per session so the page isn't always
            // blowing the same direction. Combined with each drop's
            // own random drift, this is what makes the rain look
            // natural — a coherent wind with individual variation,
            // not 200 drops all falling on parallel rails.
            const windX = (Math.random() - 0.5) * 80;
            // slideY is the per-drop threshold where the drop "hits"
            // the glass. Picking from 0.62..0.82 of canvas height per
            // drop means different drops stick to the glass at
            // different times — some streaks race to the bottom,
            // others barely get going before fading out. This is
            // visually much more interesting than a hard single line.
            return {
                x: Math.random() * this._w,
                y: Math.random() * this._h,
                // Bigger speed range than before (180..560 px/s) — a
                // mix of fast streaks and slow fat drops reads as
                // realistic rainfall.
                speed: 180 + Math.random() * 380,
                // Per-drop own drift on top of the global wind.
                drift: (Math.random() - 0.5) * 40,
                // Each drop's tilt while FALLING — a few degrees off
                // vertical because of the wind. Picked in radians;
                // ~0.10..0.30 rad = 5°..17° tilt. Sliding flips it.
                tilt: 0.10 + Math.random() * 0.20,
                // Per-drop slide trigger. Some drops stick early
                // (slideY 0.62), others travel almost the full page
                // before sticking (slideY 0.82).
                slideY: 0.62 + Math.random() * 0.20,
                // Direction & magnitude of the slide phase.
                slideDrift: 60 + Math.random() * 80,
                // Per-drop size and base alpha.
                size: 0.55 + Math.random() * 0.9,
                alpha: 0.30 + Math.random() * 0.50,
                // 'falling' → vertical-ish raindrop at full alpha.
                // 'sliding' → drop has "hit" the transparent glass and
                // runs sideways while alpha fades. Respawned at top
                // when alpha drops below 0.05 — no puddling.
                phase: 'falling',
                // Per-drop phase progress for sliding tilt transition
                // (0..1, lerps from drop's natural tilt to ~70°).
                slideProgress: 0,
                // Cached rotation (recomputed each frame, kept here so
                // the render path is one read).
                rotation: 0,
                // Store the wind so the respawn path can re-bake with
                // the same wind (consistency within a drop's life).
                _wind: windX,
            };
        }

        _drawRain(dt) {
            const sprite = this._sprites.rain;
            const sw = sprite.width, sh = sprite.height;
            // Two soft slide lines: drops transition gradually over a
            // 0.06-tall band centered at their personal slideY, so
            // there's no "snap to 50°" moment — the tilt and alpha
            // interpolate over a few frames as the drop reaches the
            // glass. Per-drop random slideY also staggers the
            // transition across drops.
            for (let i = 0; i < this._particles.length; i++) {
                const p = this._particles[i];
                if (p.phase === 'falling') {
                    // Slight wind-driven tilt: the drop doesn't fall
                    // straight down — it leans with the wind. Larger
                    // wind → more tilt (capped so it never looks
                    // like horizontal rain).
                    p.y += p.speed * dt;
                    p.x += (p.drift + p._wind) * dt;
                    // Tilt during falling = base tilt + a small
                    // contribution from wind so adjacent drops don't
                    // fall in perfect parallel.
                    p.rotation = p.tilt + (p._wind * 0.0015);
                    // Check if the drop is approaching its personal
                    // glass surface. We don't snap to sliding — we
                    // start the slideProgress ramp once y crosses
                    // slideY - 0.03, so the drop visibly LEANS onto
                    // the glass over a few frames.
                    const sy = this._h * p.slideY;
                    if (p.y >= sy - this._h * 0.03) {
                        p.phase = 'sliding';
                        p.slideProgress = 0;
                    }
                } else {
                    // sliding: continue y downward a tiny amount, big
                    // x drift, alpha fades. Rotation interpolates
                    // from the drop's natural tilt to ~70° (almost
                    // horizontal) over a 0.18-tall band, so the
                    // "sticking to glass" moment is smooth, not a snap.
                    p.x += p.slideDrift * dt;
                    p.y += p.speed * 0.06 * dt;
                    p.slideProgress = Math.min(1, p.slideProgress + dt / 0.45);
                    // rotation easeOutCubic: starts gentle, locks
                    // into the final tilt at the end of the slide.
                    const eased = 1 - Math.pow(1 - p.slideProgress, 3);
                    p.rotation = p.tilt + eased * (Math.PI * 0.40 - p.tilt);
                    // Alpha fades from 0.55 to 0 over the slide band.
                    // We cap at 0.55 so a drop still looks like water,
                    // not a ghostly thread, while moving.
                    p.alpha = Math.max(0, 0.55 * (1 - p.slideProgress));
                }
                // Wrap horizontally during falling phase so drops
                // near the edge don't disappear prematurely. Sliding
                // drops do NOT wrap — they streak off the screen,
                // matching the "drops running down a real window" feel.
                if (p.phase === 'falling') {
                    if (p.x < -sw * 2) p.x = this._w + sw;
                    if (p.x > this._w + sw * 2) p.x = -sw;
                }
                // Respawn when faded out (sliding) or fell off the
                // top of the canvas after a wrap, or drifted past the
                // bottom edge entirely.
                if (p.alpha <= 0.05 || p.y > this._h + sh || p.x < -sh * 2 || p.x > this._w + sh * 2) {
                    p.x = Math.random() * this._w;
                    p.y = -sh - Math.random() * sh;
                    p.speed = 180 + Math.random() * 380;
                    p.drift = (Math.random() - 0.5) * 40;
                    p.tilt = 0.10 + Math.random() * 0.20;
                    p.slideY = 0.62 + Math.random() * 0.20;
                    p.slideDrift = 60 + Math.random() * 80;
                    p.size = 0.55 + Math.random() * 0.9;
                    p.alpha = 0.30 + Math.random() * 0.50;
                    p.phase = 'falling';
                    p.slideProgress = 0;
                    continue;
                }
                // Render with per-drop rotation. The vertical sprite
                // reads naturally as a wind-blown raindrop at the
                // falling tilt (~5-17°), and as a horizontal streak
                // (70°) while sliding.
                this._ctx.save();
                this._ctx.translate(p.x, p.y);
                this._ctx.rotate(p.rotation);
                this._ctx.globalAlpha = p.alpha;
                this._ctx.drawImage(sprite,
                    -sw * p.size / 2,
                    -sh * p.size / 2,
                    sw * p.size, sh * p.size);
                this._ctx.restore();
            }
            this._ctx.globalAlpha = 1;
        }

        // ── 阳光光斑 (lens flare 风格) ─────────────────
        // 4 个光点 (>=50 density) 或 2 个 (<50) — 比之前 2/1 多,
        // 多个 lens flare 叠加时形成更"有光感"的玻璃表面.
        // 速度更慢 (0.0001~0.0003 vs 之前 0.0002~0.0005), 让
        // 用户能盯着光斑看而不是"飞过去".
        _setupSunlight() {
            const n = this._density > 50 ? 4 : 2;
            // sprite 400 而非 600: 之前 600 太大, alpha 0.30 太淡, 视觉上
            // 几乎透明. 400 让光柱 + 中心更紧凑, 配更高的 baseAlpha
            // 让光斑真的"看得见".
            if (!this._sprites.sun) this._sprites.sun = makeSunSprite(400);
            this._particles = [];
            for (let i = 0; i < n; i++) {
                // Size 0.55..1.5 范围更大, 大小光斑混合像真实阳光
                // 透过云缝洒下来的"光团大小不一".
                this._particles.push({
                    x: Math.random() * this._w,
                    y: Math.random() * this._h,
                    tx: Math.random() * this._w,
                    ty: Math.random() * this._h,
                    // 极慢漂移 — 让光斑感觉"挂在玻璃上", 不是飞快移动
                    speed: 0.0001 + Math.random() * 0.0002,
                    size: 0.55 + Math.random() * 0.95,
                    // 基础 alpha 0.50..0.70 — 之前 0.30 太淡. 阳光在玻璃
                    // 上光晕本身就该"明亮", 但光柱和散光让整体仍柔和.
                    baseAlpha: 0.50 + Math.random() * 0.20,
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
                if (Math.hypot(dx, dy) < 12) {
                    p.tx = Math.random() * this._w;
                    p.ty = Math.random() * this._h;
                } else {
                    p.x += dx * p.speed * dt * 60;
                    p.y += dy * p.speed * dt * 60;
                }
                // 用 baseAlpha, 让大小光斑亮度不同 — 大光斑稍亮, 小
                // 光斑稍暗, 跟 size 相关 (0.5..1.4).
                this._ctx.globalAlpha = p.baseAlpha * (0.6 + p.size * 0.4);
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
