import { existsSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const LEGACY_PI_MODULES_SPECIFIER = "omp-legacy-pi-modules";
const VIRTUAL_NAMESPACE = "substrate-legacy-pi-modules-build";

const BUNDLED_PACKAGES = [
  { name: "@oh-my-pi/pi-agent-core", identifier: "PiAgentCore", rootShim: null },
  { name: "@oh-my-pi/pi-ai", identifier: "PiAi", rootShim: "legacy-pi-ai-shim.ts" },
  {
    name: "@oh-my-pi/pi-coding-agent",
    identifier: "PiCodingAgent",
    rootShim: "legacy-pi-coding-agent-shim.ts",
  },
  { name: "@oh-my-pi/pi-natives", identifier: "PiNatives", rootShim: null },
  { name: "@oh-my-pi/pi-tui", identifier: "PiTui", rootShim: "legacy-pi-tui-shim.ts" },
  { name: "@oh-my-pi/pi-utils", identifier: "PiUtils", rootShim: null },
] as const;

const SKIPPED_WILDCARD_BASENAMES: Record<string, true> = { index: true };
const MAIN_THREAD_UNSAFE_WILDCARD_BASENAMES: Record<string, true> = { "worker-entry": true };
const SOURCE_SUFFIXES = [".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs", ".jsx"];

type PackageManifest = {
  name?: unknown;
  exports?: unknown;
};

type BundledPiEntry = {
  key: string;
  binding: string;
  importSpecifier: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function bindingForSubpath(identifier: string, subpath: string): string {
  const segments = subpath
    .split("/")
    .filter(Boolean)
    .map((segment) =>
      segment
        .split(/[-_]/)
        .filter(Boolean)
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join(""),
    );
  return `bundled${identifier}${segments.join("")}`;
}

function exportImportTarget(value: unknown): string | null {
  if (typeof value === "string") return value;
  if (isRecord(value) && typeof value.import === "string") return value.import;
  return null;
}

function parseWildcardPattern(exportKey: string, sourcePattern: string) {
  const exportStar = exportKey.indexOf("*");
  const sourceStar = sourcePattern.indexOf("*");
  if (exportStar === -1 || sourceStar === -1) return null;
  if (exportKey.indexOf("*", exportStar + 1) !== -1) return null;
  if (sourcePattern.indexOf("*", sourceStar + 1) !== -1) return null;
  if (!sourcePattern.startsWith("./")) return null;
  return {
    exportPrefix: exportKey.slice(2, exportStar),
    exportSuffix: exportKey.slice(exportStar + 1),
    sourcePrefix: sourcePattern.slice(2, sourceStar),
    sourceSuffix: sourcePattern.slice(sourceStar + 1),
  };
}

async function installedPackage(
  name: string,
): Promise<{ root: string; manifest: PackageManifest }> {
  const resolvedEntrypoint = import.meta.resolve(name);
  const entrypoint = resolvedEntrypoint.startsWith("file:")
    ? fileURLToPath(resolvedEntrypoint)
    : path.resolve(resolvedEntrypoint);
  let directory = path.dirname(entrypoint);
  while (true) {
    const manifestPath = path.join(directory, "package.json");
    const file = Bun.file(manifestPath);
    if (await file.exists()) {
      const manifest = (await file.json()) as PackageManifest;
      if (manifest.name === name) return { root: directory, manifest };
    }
    const parent = path.dirname(directory);
    if (parent === directory) break;
    directory = parent;
  }
  throw new Error(`Could not locate installed package ${name} from ${entrypoint}`);
}

async function collectBundledPiEntries(): Promise<BundledPiEntry[]> {
  const entries: BundledPiEntry[] = [];
  const seenKeys = new Set<string>();
  const seenBindings = new Set<string>();
  const codingAgent = await installedPackage("@oh-my-pi/pi-coding-agent");

  function addEntry(key: string, binding: string, importSpecifier: string): void {
    if (seenKeys.has(key)) return;
    if (seenBindings.has(binding)) {
      throw new Error(`Duplicate bundled Pi binding ${binding} for ${key}`);
    }
    seenKeys.add(key);
    seenBindings.add(binding);
    entries.push({
      key,
      binding,
      importSpecifier: importSpecifier.startsWith("@oh-my-pi/")
        ? fileURLToPath(import.meta.resolve(importSpecifier))
        : importSpecifier,
    });
  }

  for (const pkg of BUNDLED_PACKAGES) {
    const { root, manifest } = await installedPackage(pkg.name);
    if (typeof manifest.name !== "string") {
      throw new Error(`Installed package manifest has no name: ${path.join(root, "package.json")}`);
    }
    const exportsField = isRecord(manifest.exports) ? manifest.exports : {};
    const rootSpecifier = pkg.rootShim
      ? path.join(codingAgent.root, "src", "extensibility", pkg.rootShim)
      : manifest.name;
    addEntry(manifest.name, `bundled${pkg.identifier}`, rootSpecifier);

    for (const exportKey in exportsField) {
      if (!exportKey.startsWith("./") || exportKey === "." || exportKey.includes("*")) continue;
      const subpath = exportKey.slice(2);
      addEntry(
        `${manifest.name}/${subpath}`,
        bindingForSubpath(pkg.identifier, subpath),
        `${manifest.name}/${subpath}`,
      );
    }

    for (const exportKey in exportsField) {
      if (!exportKey.startsWith("./") || exportKey === "." || !exportKey.includes("*")) continue;
      const sourcePattern = exportImportTarget(exportsField[exportKey]);
      if (!sourcePattern) continue;
      const pattern = parseWildcardPattern(exportKey, sourcePattern);
      if (
        !pattern ||
        !SOURCE_SUFFIXES.some((suffix) => pattern.sourceSuffix.endsWith(suffix)) ||
        pattern.exportPrefix === "" ||
        pattern.exportPrefix === "/"
      ) {
        continue;
      }

      const sourceDir = path.join(root, pattern.sourcePrefix);
      if (!existsSync(sourceDir)) continue;
      const glob = new Bun.Glob(`**/*${pattern.sourceSuffix}`);
      const matches: string[] = [];
      for await (const match of glob.scan({ cwd: sourceDir, onlyFiles: true })) {
        matches.push(match.split(path.sep).join("/"));
      }
      matches.sort();
      for (const match of matches) {
        if (!match.endsWith(pattern.sourceSuffix)) continue;
        const basename = match.slice(0, match.length - pattern.sourceSuffix.length);
        const segments = basename.split("/");
        if (segments.some((segment) => segment.startsWith(".") || segment.startsWith("_")))
          continue;
        const wildcardBasename = segments.at(-1) ?? "";
        if (
          !wildcardBasename ||
          SKIPPED_WILDCARD_BASENAMES[wildcardBasename] === true ||
          MAIN_THREAD_UNSAFE_WILDCARD_BASENAMES[wildcardBasename] === true ||
          /\.(test|spec|d|generated|bench)$/.test(wildcardBasename)
        ) {
          continue;
        }
        const subpath = `${pattern.exportPrefix}${basename}${pattern.exportSuffix}`;
        const key = `${manifest.name}/${subpath}`;
        addEntry(key, bindingForSubpath(pkg.identifier, subpath), key);
      }
    }
  }

  const typeboxShim = path.join(codingAgent.root, "src", "extensibility", "legacy-typebox.ts");
  addEntry("typebox", "bundledTypeBoxShim", typeboxShim);
  return entries;
}

function renderLegacyPiVirtualModule(entries: readonly BundledPiEntry[]): string {
  const loaders = entries.map(
    (entry) => `const ${entry.binding} = () => import(${JSON.stringify(entry.importSpecifier)});`,
  );
  const modules = entries.map((entry) => `\t${JSON.stringify(entry.key)}: ${entry.binding},`);
  return [...loaders, "", "export const BUNDLED_PI_MODULE_LOADERS = {", ...modules, "};", ""].join(
    "\n",
  );
}

function legacyPiVirtualModulePlugin(source: string): Bun.BunPlugin {
  return {
    name: "substrate:legacy-pi-modules",
    setup(build) {
      build.onResolve({ filter: /^omp-legacy-pi-modules$/ }, () => ({
        path: LEGACY_PI_MODULES_SPECIFIER,
        namespace: VIRTUAL_NAMESPACE,
      }));
      build.onLoad({ filter: /.*/, namespace: VIRTUAL_NAMESPACE }, () => ({
        contents: source,
        loader: "ts",
      }));
    },
  };
}

async function main(): Promise<void> {
  const [target, requestedOutfile, ...extraArgs] = process.argv.slice(2);
  if (!target || !requestedOutfile || extraArgs.length > 0) {
    throw new Error("Usage: bun run bridge/build-omp-bridge.ts <bun-target> <outfile>");
  }

  const outfile = path.resolve(requestedOutfile);
  await mkdir(path.dirname(outfile), { recursive: true });
  const entries = await collectBundledPiEntries();
  const source = renderLegacyPiVirtualModule(entries);
  const result = await Bun.build({
    entrypoints: [path.join(import.meta.dir, "omp-bridge.ts")],
    root: import.meta.dir,
    define: {
      "process.env.PI_COMPILED": JSON.stringify("true"),
    },
    plugins: [legacyPiVirtualModulePlugin(source)],
    compile: {
      target: target as Bun.Build.CompileTarget,
      outfile,
    },
    throw: false,
  });
  if (!result.success) {
    throw new Error(
      `OMP bridge binary bundle failed:\n${result.logs.map((log) => log.message).join("\n")}`,
    );
  }
}

await main();
