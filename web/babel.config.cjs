module.exports = {
  presets: [
    ["@babel/preset-env", { targets: { node: "24" }, modules: "commonjs" }],
    ["@babel/preset-react", { runtime: "automatic" }],
    "@babel/preset-typescript",
  ],
};
