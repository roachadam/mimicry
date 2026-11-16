// Lint for the SDK, its tests and scripts: typescript-eslint's strict,
// type-checked rules. The generated code (src/gen) and the build are not
// linted.
import js from "@eslint/js";
import { defineConfig } from "eslint/config";
import tseslint from "typescript-eslint";

export default defineConfig(
  {
    ignores: ["dist/", "build/", "src/gen/", ".core/", "node_modules/"],
  },
  js.configs.recommended,
  tseslint.configs.strictTypeChecked,
  {
    languageOptions: {
      parserOptions: {
        projectService: {
          allowDefaultProject: ["eslint.config.js"],
        },
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      "@typescript-eslint/restrict-template-expressions": ["error", { allowNumber: true, allowBoolean: true }],
      // Kind, Priority, ... are the schema's enum values (Go's) by design,
      // cast to the generated enums where a message takes them.
      "@typescript-eslint/no-unsafe-enum-assignment": "off",
      // Promise.withResolvers<void>() and the like.
      "@typescript-eslint/no-invalid-void-type": "off",
      "@typescript-eslint/no-floating-promises": [
        "error",
        {
          allowForKnownSafeCalls: [{ from: "package", package: "node:test", name: ["test", "describe", "it", "suite"] }],
        },
      ],
    },
  },
);
