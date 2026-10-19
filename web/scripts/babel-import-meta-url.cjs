// Jest runs modules as CommonJS, where `import.meta` does not exist. Some
// ESM-only test dependencies (msw 3's interceptors) read `import.meta.url`
// to find files next to themselves; this rewrites exactly that expression
// to the CommonJS equivalent. Babel only runs under Jest: Vite builds the
// app, so production code never passes through this plugin.
module.exports = function importMetaUrl({ types: t, template }) {
  const replacement = template.expression.ast(
    'require("node:url").pathToFileURL(__filename).href',
  );
  return {
    name: "import-meta-url",
    visitor: {
      MemberExpression(path) {
        const { node } = path;
        const isImportMeta =
          t.isMetaProperty(node.object) &&
          node.object.meta.name === "import" &&
          node.object.property.name === "meta";
        if (
          isImportMeta &&
          !node.computed &&
          t.isIdentifier(node.property, { name: "url" })
        ) {
          path.replaceWith(t.cloneNode(replacement));
        }
      },
    },
  };
};
