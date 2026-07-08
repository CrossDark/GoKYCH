(function () {
    'use strict';

    const STORAGE_KEYS = {
        noiseIntensity: 'gokych:abyss:noise_intensity',
        glowIntensity: 'gokych:abyss:glow_intensity',
        borderGlow: 'gokych:abyss:border_glow',
        moldEffect: 'gokych:abyss:mold_effect',
        accentMode: 'gokych:abyss:accent_mode',
    };

    const DEFAULT_VALUES = {
        noiseIntensity: 30,
        glowIntensity: 40,
        borderGlow: 60,
        moldEffect: 'subtle',
        accentMode: 'dual',
    };

    function shouldEnableEffects() {
        try {
            if (window.matchMedia &&
                window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
                return false;
            }
            const dm = navigator.deviceMemory;
            if (typeof dm === 'number' && dm < 2) return false;
        } catch (e) {}
        return true;
    }

    class AbyssFX {
        constructor() {
            this._layer = null;
            this._canvas = null;
            this._ctx = null;
            this._enabled = shouldEnableEffects();
            this._config = { ...DEFAULT_VALUES };
            this._moldCanvas = null;
            this._moldCtx = null;
            this._rafId = null;
            this._phase = 0;
            this._init();
        }

        _init() {
            this._loadConfig().then(() => {
                this._applyConfig();
                if (this._enabled) {
                    this._createLayer();
                    if (this._config.moldEffect !== 'none') {
                        this._setupMold();
                    }
                }
            });
        }

        async _loadConfig() {
            try {
                const r = await fetch('/api/themes/abyss-stage/settings', { cache: 'no-store' });
                if (!r.ok) throw new Error('settings HTTP ' + r.status);
                const data = await r.json();
                const schema = (data && data.schema) || [];
                const values = (data && data.values) || {};
                const def = (k) => {
                    const e = schema.find((s) => s.key === k);
                    return e ? e.default : undefined;
                };

                const noiseVal = values.noise_intensity != null
                    ? parseInt(values.noise_intensity, 10)
                    : (typeof def('noise_intensity') === 'number' ? def('noise_intensity') : NaN);
                if (!isNaN(noiseVal)) this._config.noiseIntensity = Math.max(0, Math.min(100, noiseVal));

                const glowVal = values.glow_intensity != null
                    ? parseInt(values.glow_intensity, 10)
                    : (typeof def('glow_intensity') === 'number' ? def('glow_intensity') : NaN);
                if (!isNaN(glowVal)) this._config.glowIntensity = Math.max(0, Math.min(100, glowVal));

                const borderVal = values.border_glow != null
                    ? parseInt(values.border_glow, 10)
                    : (typeof def('border_glow') === 'number' ? def('border_glow') : NaN);
                if (!isNaN(borderVal)) this._config.borderGlow = Math.max(0, Math.min(100, borderVal));

                const moldVal = values.mold_effect != null ? values.mold_effect : def('mold_effect');
                if (moldVal === 'none' || moldVal === 'subtle' || moldVal === 'moderate') {
                    this._config.moldEffect = moldVal;
                }

                const accentVal = values.accent_mode != null ? values.accent_mode : def('accent_mode');
                if (accentVal === 'gold' || accentVal === 'green' || accentVal === 'dual') {
                    this._config.accentMode = accentVal;
                }

                return;
            } catch (e) {
                this._loadFromStorage();
            }
        }

        _loadFromStorage() {
            const n = parseInt(localStorage.getItem(STORAGE_KEYS.noiseIntensity) || '', 10);
            if (!isNaN(n)) this._config.noiseIntensity = Math.max(0, Math.min(100, n));

            const g = parseInt(localStorage.getItem(STORAGE_KEYS.glowIntensity) || '', 10);
            if (!isNaN(g)) this._config.glowIntensity = Math.max(0, Math.min(100, g));

            const b = parseInt(localStorage.getItem(STORAGE_KEYS.borderGlow) || '', 10);
            if (!isNaN(b)) this._config.borderGlow = Math.max(0, Math.min(100, b));

            const m = localStorage.getItem(STORAGE_KEYS.moldEffect);
            if (m === 'none' || m === 'subtle' || m === 'moderate') this._config.moldEffect = m;

            const a = localStorage.getItem(STORAGE_KEYS.accentMode);
            if (a === 'gold' || a === 'green' || a === 'dual') this._config.accentMode = a;
        }

        _applyConfig() {
            this.setNoiseIntensity(this._config.noiseIntensity);
            this.setGlowIntensity(this._config.glowIntensity);
            this.setBorderGlow(this._config.borderGlow);
            this.setMoldEffect(this._config.moldEffect);
            this.setAccentMode(this._config.accentMode);
        }

        _createLayer() {
            let layer = document.getElementById('abyss-fx-layer');
            if (!layer) {
                layer = document.createElement('div');
                layer.id = 'abyss-fx-layer';
                layer.setAttribute('aria-hidden', 'true');
                document.body.appendChild(layer);
            }
            this._layer = layer;
        }

        _setupMold() {
            const canvas = document.createElement('canvas');
            canvas.setAttribute('aria-hidden', 'true');
            canvas.style.position = 'absolute';
            canvas.style.inset = '0';
            canvas.style.width = '100%';
            canvas.style.height = '100%';
            this._layer.appendChild(canvas);
            this._moldCanvas = canvas;
            this._moldCtx = canvas.getContext('2d', { alpha: true });
            this._resizeMold();

            if (typeof ResizeObserver !== 'undefined') {
                new ResizeObserver(() => this._resizeMold()).observe(document.documentElement);
            } else {
                window.addEventListener('resize', () => this._resizeMold());
            }

            this._scheduleMold();
        }

        _resizeMold() {
            if (!this._moldCanvas) return;
            const dpr = Math.min(window.devicePixelRatio || 1, 2);
            const w = window.innerWidth;
            const h = window.innerHeight;
            this._moldCanvas.width = Math.floor(w * dpr);
            this._moldCanvas.height = Math.floor(h * dpr);
            this._moldCanvas.style.width = w + 'px';
            this._moldCanvas.style.height = h + 'px';
            this._moldCtx.setTransform(dpr, 0, 0, dpr, 0, 0);
        }

        _drawMold() {
            if (!this._moldCtx || !this._moldCanvas) return;
            const ctx = this._moldCtx;
            const w = window.innerWidth;
            const h = window.innerHeight;
            const intensity = this._config.moldEffect === 'subtle' ? 0.03 : 0.06;

            ctx.clearRect(0, 0, w, h);
            ctx.globalCompositeOperation = 'screen';

            const corners = [
                { x: 0, y: 0 },
                { x: w, y: 0 },
                { x: 0, y: h },
                { x: w, y: h },
            ];

            for (let i = 0; i < corners.length; i++) {
                const corner = corners[i];
                const size = Math.min(w, h) * 0.35;
                const offset = Math.sin(this._phase + i * Math.PI / 2) * 10;

                const g = ctx.createRadialGradient(
                    corner.x, corner.y, 0,
                    corner.x, corner.y, size
                );
                g.addColorStop(0, `rgba(46, 139, 87, ${intensity})`);
                g.addColorStop(0.5, `rgba(27, 77, 62, ${intensity * 0.5})`);
                g.addColorStop(1, 'rgba(0, 0, 0, 0)');

                ctx.fillStyle = g;
                ctx.beginPath();
                ctx.ellipse(
                    corner.x + (i % 2 === 0 ? offset : -offset),
                    corner.y + (i < 2 ? offset : -offset),
                    size * 0.7,
                    size * 0.7,
                    0, 0, Math.PI * 2
                );
                ctx.fill();
            }

            ctx.globalCompositeOperation = 'source-over';
            this._phase += 0.003;
        }

        _scheduleMold() {
            if (this._rafId) return;
            this._rafId = requestAnimationFrame(() => {
                this._rafId = null;
                if (document.hidden) {
                    this._scheduleMold();
                    return;
                }
                this._drawMold();
                this._scheduleMold();
            });
        }

        setNoiseIntensity(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            this._config.noiseIntensity = v;
            localStorage.setItem(STORAGE_KEYS.noiseIntensity, String(v));
            document.documentElement.style.setProperty('--noise-opacity', String(v / 1000));
        }

        setGlowIntensity(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            this._config.glowIntensity = v;
            localStorage.setItem(STORAGE_KEYS.glowIntensity, String(v));
            document.documentElement.style.setProperty('--glow-strength', String(v / 100));
        }

        setBorderGlow(value) {
            const v = Math.max(0, Math.min(100, value | 0));
            this._config.borderGlow = v;
            localStorage.setItem(STORAGE_KEYS.borderGlow, String(v));
            document.documentElement.style.setProperty('--border-glow-strength', String(v / 100));
        }

        setMoldEffect(mode) {
            if (mode !== 'none' && mode !== 'subtle' && mode !== 'moderate') return;
            this._config.moldEffect = mode;
            localStorage.setItem(STORAGE_KEYS.moldEffect, mode);

            if (mode === 'none') {
                if (this._moldCanvas) {
                    this._moldCanvas.remove();
                    this._moldCanvas = null;
                    this._moldCtx = null;
                }
                if (this._rafId) {
                    cancelAnimationFrame(this._rafId);
                    this._rafId = null;
                }
            } else if (this._enabled && !this._moldCanvas) {
                this._setupMold();
            }
        }

        setAccentMode(mode) {
            if (mode !== 'gold' && mode !== 'green' && mode !== 'dual') return;
            this._config.accentMode = mode;
            localStorage.setItem(STORAGE_KEYS.accentMode, mode);

            const root = document.documentElement;
            if (mode === 'gold') {
                root.style.setProperty('--accent', '#C9A227');
                root.style.setProperty('--link-color', '#C9A227');
            } else if (mode === 'green') {
                root.style.setProperty('--accent', '#2E8B57');
                root.style.setProperty('--link-color', '#2E8B57');
            } else {
                root.style.removeProperty('--accent');
                root.style.removeProperty('--link-color');
            }
        }

        async reload() {
            try {
                const r = await fetch('/api/themes/abyss-stage/settings', { cache: 'no-store' });
                if (!r.ok) return false;
                const data = await r.json();
                const schema = (data && data.schema) || [];
                const values = (data && data.values) || {};
                const def = (k) => {
                    const e = schema.find((s) => s.key === k);
                    return e ? e.default : undefined;
                };

                const noiseVal = values.noise_intensity != null
                    ? parseInt(values.noise_intensity, 10)
                    : (typeof def('noise_intensity') === 'number' ? def('noise_intensity') : NaN);
                if (!isNaN(noiseVal)) this.setNoiseIntensity(noiseVal);

                const glowVal = values.glow_intensity != null
                    ? parseInt(values.glow_intensity, 10)
                    : (typeof def('glow_intensity') === 'number' ? def('glow_intensity') : NaN);
                if (!isNaN(glowVal)) this.setGlowIntensity(glowVal);

                const borderVal = values.border_glow != null
                    ? parseInt(values.border_glow, 10)
                    : (typeof def('border_glow') === 'number' ? def('border_glow') : NaN);
                if (!isNaN(borderVal)) this.setBorderGlow(borderVal);

                const moldVal = values.mold_effect != null ? values.mold_effect : def('mold_effect');
                if (moldVal === 'none' || moldVal === 'subtle' || moldVal === 'moderate') {
                    this.setMoldEffect(moldVal);
                }

                const accentVal = values.accent_mode != null ? values.accent_mode : def('accent_mode');
                if (accentVal === 'gold' || accentVal === 'green' || accentVal === 'dual') {
                    this.setAccentMode(accentVal);
                }

                return true;
            } catch (e) {
                return false;
            }
        }
    }

    function mount() {
        if (window.AbyssFX) return;
        window.AbyssFX = new AbyssFX();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', mount, { once: true });
    } else {
        mount();
    }
})();