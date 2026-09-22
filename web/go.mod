// This file makes web/ a separate (empty) Go module so that the root module's
// `./...` patterns never descend into node_modules, where third-party npm
// packages occasionally ship Go sources. There is no Go code here; the build
// output is embedded from internal/api/web/dist (see vite.config.ts).
module github.com/BonzTM/bloom/web

go 1.26
