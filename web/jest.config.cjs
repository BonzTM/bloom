/** @type {import("jest").Config} */
module.exports = {
  testEnvironment: "jsdom",
  testMatch: ["<rootDir>/src/**/*.test.{ts,tsx}"],
  setupFiles: ["<rootDir>/src/test/polyfills.cjs"],
  transform: { "^.+\\.[cm]?[jt]sx?$": "babel-jest" },
  transformIgnorePatterns: [
    "node_modules/(?!(@bundled-es-modules|@mswjs|@open-draft|msw|rettime|until-async)/)",
  ],
  moduleNameMapper: {
    "^(\\.{1,2}/.*)\\.js$": "$1",
    "\\.css$": "<rootDir>/src/test/style-mock.cjs",
  },
  setupFilesAfterEnv: ["<rootDir>/src/test/setup.ts"],
  clearMocks: true,
  restoreMocks: true,
  maxWorkers: 2,
};
