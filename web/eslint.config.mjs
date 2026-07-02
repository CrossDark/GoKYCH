import js from "@eslint/js";
import tseslint from "typescript-eslint";
import nextCoreWebVitals from "eslint-config-next/core-web-vitals";

const config = [
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...nextCoreWebVitals,
  {
    ignores: [
      "node_modules/",
      ".next/",
      "out/",
      "build/",
    ],
  },
];

export default config;
