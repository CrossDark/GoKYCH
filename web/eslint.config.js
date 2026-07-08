const nextConfig = require("eslint-config-next");

module.exports = [
  ...nextConfig,
  {
    ignores: [
      "node_modules/",
      ".next/",
      ".open-next/",
      "dist/",
      "out/",
      "data/themes/",
      "opt/themes/",
      "*.tmp",
      "*.log",
    ],
  },
  {
    files: ["app/bbcode/[slug]/page.tsx", "app/html/[slug]/page.tsx", "app/md/[slug]/page.tsx", "app/typst/[slug]/page.tsx", "app/wikidot/[slug]/page.tsx"],
    rules: {
      "react-hooks/error-boundaries": "off",
    },
  },
];