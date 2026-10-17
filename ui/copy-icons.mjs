import { cp, mkdir } from "node:fs/promises";
import { dirname, join } from "node:path";

const source = join(process.cwd(), "node_modules", "@tabler", "icons", "icons", "outline");
const target = join(process.cwd(), "..", "internal", "web", "assets", "icons");
const names = [
  "layout-dashboard", "server-2", "route", "world", "shield-check", "users", "link",
  "list-check", "settings", "book", "help-circle", "user-circle", "menu-2", "chevron-down",
  "arrow-right", "plus", "refresh", "activity", "database", "alert-triangle", "circle-check"
];
await mkdir(target, { recursive: true });
for (const name of names) {
  await cp(join(source, `${name}.svg`), join(target, `${name}.svg`));
}

// Bundle the API renderer locally; control has no runtime CDN dependency.
const docs = join(process.cwd(), '..', 'internal', 'web', 'assets', 'api');
await mkdir(docs, {recursive: true});
await cp(join(process.cwd(), 'node_modules', '@scalar', 'api-reference', 'dist', 'browser', 'standalone.js'), join(docs, 'scalar.js'));
