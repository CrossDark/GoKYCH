const fs = require("fs");
const path = require("path");

const workerName = process.env.WORKER_NAME || process.env.CF_WORKER_NAME || "ywda-web";

const config = {
  // Cloudflare Workers 配置文件（由 scripts/gen-wrangler-config.cjs 自动生成）
  // ──────────────────────────────────────────────────────────
  // Worker 名称通过环境变量 WORKER_NAME / CF_WORKER_NAME 设置，
  // 默认值 "ywda-web"。name 和 services[0].service 始终保持一致，
  // 避免 Cloudflare CI 覆盖 name 后 service 指向旧名称导致的 10143 错误。
  //
  // Cloudflare Workers Builds（Git 连接部署）会自动用 Dashboard 中的
  // Worker 名称覆盖 name 字段，但不会覆盖 services[].service，因此
  // 本脚本确保两个字段始终使用同一个值。
  //
  // 用法：
  //   WORKER_NAME=my-worker npm run cf:deploy   # 自定义 Worker 名称
  //   npm run cf:deploy                         # 使用默认值 ywda-web
  //
  // 构建环境变量（不在此文件中设置）：
  //   NEXT_PUBLIC_API_BASE_URL — Next.js 构建时变量，通过 .env.local 或
  //                              CI 环境变量传入

  $schema: "node_modules/wrangler/config-schema.json",
  name: workerName,
  main: ".open-next/worker.js",
  compatibility_date: "2026-03-01",
  compatibility_flags: ["nodejs_compat", "global_fetch_strictly_public"],
  assets: {
    directory: ".open-next/assets",
    binding: "ASSETS",
  },
  services: [
    { binding: "WORKER_SELF_REFERENCE", service: workerName },
  ],
  observability: { enabled: true },
};

function toJSONC(obj, indent = 0) {
  const pad = "  ".repeat(indent);
  const padInner = "  ".repeat(indent + 1);
  let out = "{\n";
  const entries = Object.entries(obj);
  entries.forEach(([key, value], i) => {
    // Add a blank line before major sections for readability
    if (["$schema", "name", "main", "compatibility_date", "compatibility_flags", "assets", "services", "observability"].includes(key)) {
      const comments = {
        $schema: "",
        name: `\n${padInner}// Worker 名称（来自环境变量 WORKER_NAME，默认 ywda-web）`,
        main: "",
        compatibility_date: "",
        compatibility_flags: "",
        assets: "",
        services: `\n${padInner}// WORKER_SELF_REFERENCE 是 OpenNext ISR/缓存自调用绑定，必须指向自身`,
        observability: "",
      };
      if (comments[key]) out += comments[key] + "\n";
    }

    out += `${padInner}"${key}": `;
    if (Array.isArray(value)) {
      out += "[";
      if (value.length > 0 && typeof value[0] === "object") {
        out += "\n";
        value.forEach((item, j) => {
          out += `${padInner}  `;
          const itemEntries = Object.entries(item);
          out += "{ ";
          out += itemEntries.map(([k, v]) => `"${k}": ${JSON.stringify(v)}`).join(", ");
          out += " }";
          if (j < value.length - 1) out += ",";
          out += "\n";
        });
        out += `${padInner}]`;
      } else {
        out += value.map((v) => JSON.stringify(v)).join(", ");
        out += "]";
      }
    } else if (typeof value === "object" && value !== null) {
      out += "{\n";
      const ve = Object.entries(value);
      ve.forEach(([k, v], j) => {
        out += `${padInner}  "${k}": ${JSON.stringify(v)}`;
        if (j < ve.length - 1) out += ",";
        out += "\n";
      });
      out += `${padInner}}`;
    } else {
      out += JSON.stringify(value);
    }
    if (i < entries.length - 1) out += ",";
    out += "\n";
  });
  out += `${pad}}`;
  return out;
}

const header = `{
  // Cloudflare Workers 配置文件（由 scripts/gen-wrangler-config.cjs 自动生成，勿手动编辑）
  // ──────────────────────────────────────────────────────────────────────
  // Worker 名称通过环境变量 WORKER_NAME / CF_WORKER_NAME 设置，默认 "ywda-web"。
  // name 和 services[0].service 始终保持一致，避免 Cloudflare CI 覆盖 name 后
  // service 指向旧名称导致 10143 错误。
  //
  // Cloudflare Workers Builds（Git 连接部署）会自动用 Dashboard 中的 Worker
  // 名称覆盖 name 字段，但不会覆盖 services[].service，因此必须确保两个字段
  // 使用同一个值。
  //
  // 用法：
  //   WORKER_NAME=my-worker npm run cf:deploy   # 自定义 Worker 名称
  //   npm run cf:deploy                         # 使用默认值 ywda-web
  //
  // 构建环境变量 NEXT_PUBLIC_API_BASE_URL 不在此文件中设置，通过 .env.local
  // 或 CI 环境变量传入。
`;

const body = toJSONC(config, 0).replace(/^{\n/, "");
const output = header + body;

const outPath = path.resolve(__dirname, "..", "wrangler.jsonc");
fs.writeFileSync(outPath, output, "utf8");
console.log(`✓ wrangler.jsonc 已生成 (worker name: ${workerName})`);
