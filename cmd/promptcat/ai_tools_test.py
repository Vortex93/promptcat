#!/usr/bin/env python3
"""Regression tests for the embedded navigator; Python standard library only."""
import contextlib
import hashlib
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


class NavigatorTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location("navigator_under_test", Path(__file__).with_name("ai_tools.py"))
        self.nav = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.nav)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.nav.ROOT = self.root
        self.nav.PACK = self.root / ".promptcat"
        self.nav.PACK.mkdir()
        self.nav.PROJECT = self.nav.PACK / "project.json"
        self.nav.PROJECT.write_text("{}", encoding="utf-8")
        self.rows = []

    def add_source(self, name, content):
        target = self.root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        data = content.encode("utf-8")
        target.write_bytes(data)
        self.rows.append({
            "path": name, "language": self.nav.language(name), "role": self.nav.role(name),
            "lines": data.count(b"\n") + int(bool(data) and not data.endswith(b"\n")),
            "size": len(data), "sha256": hashlib.sha256(data).hexdigest(),
        })
        self.save_index()
        return target

    def save_index(self):
        (self.nav.PACK / "files.json").write_text(json.dumps({"schemaVersion": 1, "layout": "files", "files": self.rows}), encoding="utf-8")
        self.nav.SRC = None

    def command(self, *args):
        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.object(sys, "argv", ["tools.py", *args]), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            self.nav.main()
        return stdout.getvalue(), stderr.getvalue()

    def test_listing_and_targeted_read_are_lazy(self):
        missing = self.add_source("unrelated/missing.ts", "not needed\n")
        self.add_source("src/auth.ts", "export const auth = true;\r\n")
        missing.unlink()  # Any eager full-repository read would now fail.
        stdout, _ = self.command("files", "--json")
        self.assertEqual(len(stdout.splitlines()), 2)
        source = self.nav.sources()
        self.assertEqual(source.cache, {})
        self.assertIn("unrelated/missing.ts", source)
        self.assertEqual(source.cache, {})
        self.assertEqual(self.nav.read("src/auth.ts"), "export const auth = true;\r\n")
        self.assertEqual(list(source.cache), ["src/auth.ts"])
        with self.assertRaisesRegex(SystemExit, "cannot read archived source"):
            self.nav.read("unrelated/missing.ts")

    def test_search_reads_only_matching_prefix(self):
        self.add_source("elsewhere/missing.ts", "needle\n").unlink()
        self.add_source("src/auth.ts", "const needle = true;\n")
        stdout, stderr = self.command("search", "needle", "src/", "--limit", "1")
        self.assertIn("src/auth.ts:1:", stdout)
        self.assertIn("more matches may exist", stderr)
        self.assertEqual(self.nav.sources().cache, {})

    def test_search_releases_each_scanned_file_and_keeps_results(self):
        self.add_source("a.ts", "nothing here\n")
        self.add_source("b.ts", "first NEEDLE\nsecond needle\n")
        self.add_source("c.ts", "another needle\n")
        stdout, _ = self.command("search", "needle", "", "--limit", "20")
        self.assertEqual(len(stdout.splitlines()), 3)
        self.assertIn("b.ts:1:", stdout)
        self.assertIn("c.ts:1:", stdout)
        self.assertEqual(self.nav.sources().cache, {})
        stdout, stderr = self.command("search", "needle", "b.ts", "--limit", "1")
        self.assertEqual(len(stdout.splitlines()), 1)
        self.assertIn("Stopped at --limit", stderr)
        self.assertEqual(self.nav.sources().cache, {})
        stdout, _ = self.command("read", "b.ts")
        self.assertIn("second needle", stdout)

    def test_default_read_is_bounded_and_continuable(self):
        self.add_source("long.ts", "\n".join(f"line {i}" for i in range(1, 201)))
        stdout, stderr = self.command("read", "long.ts")
        self.assertEqual(len(stdout.splitlines()), 80)
        self.assertIn('read "long.ts" 81 160', stderr)
        stdout, stderr = self.command("read", "long.ts", "81", "100")
        self.assertEqual(len(stdout.splitlines()), 20)
        self.assertIn("81 | line 81", stdout)
        self.assertIn("101 180", stderr)
        stdout, _ = self.command("read", "long.ts", "--source-lines", "3")
        self.assertEqual(len(stdout.splitlines()), 3)

    def test_minified_lines_do_not_flood_output(self):
        content = "x" * 30000 + "NEEDLE" + "y" * 1000
        self.add_source("minified.js", content)
        stdout, stderr = self.command("read", "minified.js")
        self.assertLessEqual(len(stdout), 16001)
        self.assertIn("Source output truncated at line 1", stderr)
        stdout, _ = self.command("read", "minified.js", "--max-chars", "40000")
        self.assertIn(content, stdout)
        stdout, _ = self.command("search", "needle")
        self.assertIn("NEEDLE", stdout)
        self.assertIn("line preview truncated", stdout)
        self.assertLess(len(stdout), 500)

    def test_files_pagination_keeps_json_clean(self):
        for name in ("a.ts", "b.ts", "c.ts"):
            self.add_source(name, "source\n")
        stdout, stderr = self.command("files", "--json", "--limit", "1", "--offset", "1")
        self.assertEqual(json.loads(stdout)["path"], "b.ts")
        self.assertIn("--offset 2", stderr)
        self.assertEqual(self.nav.sources().cache, {})
        stdout, stderr = self.command("files", "--offset", "3")
        self.assertEqual(stdout, "")
        self.assertIn("beyond 3", stderr)

    def test_invalid_limits_and_ranges_fail_explicitly(self):
        self.add_source("a.ts", "one\ntwo\n")
        for args in (("files", "--limit", "0"), ("files", "--limit"), ("files", "--limit", "bad"),
                     ("files", "--offset", "-1"), ("read", "a.ts", "--source-lines", "0"),
                     ("read", "a.ts", "--max-chars", "0"), ("read", "a.ts", "0"),
                     ("read", "a.ts", "2", "1"), ("read", "a.ts", "3")):
            with self.subTest(args=args), self.assertRaises(SystemExit):
                self.command(*args)

    def test_inventory_takes_precedence_over_a_real_export_txt(self):
        literal = '<<<FILE: "phantom.go">>>\nnot actual source\n<<<END FILE>>>\n'
        self.add_source("export.txt", literal)
        self.add_source("real.go", "package real\n")
        self.assertNotIn("phantom.go", self.nav.sources())
        self.assertEqual(self.nav.read("export.txt"), literal)
        self.assertEqual(self.nav.read("real.go"), "package real\n")
        (self.root / "unlisted.go").write_text("not selected", encoding="utf-8")
        self.assertNotIn("unlisted.go", self.nav.sources())

    def test_legacy_export_layout_still_works(self):
        (self.root / "export.txt").write_text('<<<FILE: "legacy.go">>>\npackage legacy\n<<<END FILE>>>\n', encoding="utf-8")
        self.assertEqual(self.nav.read("legacy.go"), "package legacy")

    def test_invalid_inventory_never_falls_back_to_other_files(self):
        self.add_source("safe.ts", "safe\n")
        index_path = self.nav.PACK / "files.json"
        index_path.write_text("not json", encoding="utf-8")
        with self.assertRaisesRegex(SystemExit, "cannot read .promptcat/files.json"):
            self.nav.sources()
        for name in ("../escape.ts", "/absolute.ts", "C:/drive.ts", "a/../escape.ts", ".promptcat/evil.ts", "a\\b.ts"):
            with self.subTest(name=name):
                index = {"schemaVersion":1, "layout":"files", "files":[{"path":name, "lines":1}]}
                with self.assertRaises(SystemExit):
                    self.nav.IndexedSources(index)
        with self.assertRaisesRegex(SystemExit, "duplicate"):
            self.nav.IndexedSources({"schemaVersion":1, "layout":"files", "files":self.rows * 2})

    def test_symlink_source_is_rejected(self):
        source = self.add_source("source.ts", "safe\n")
        target = self.root / "target.ts"
        source.rename(target)
        try:
            source.symlink_to(target)
        except OSError as error:
            self.skipTest(f"symlinks unavailable: {error}")
        with self.assertRaisesRegex(SystemExit, "refusing source symlink"):
            self.nav.read("source.ts")

    def test_indexed_relationship_navigation(self):
        self.add_source("service.ts", "export function getCards() { return []; }\n")
        self.add_source("controller.ts", 'import { getCards } from "./service";\nexport function loadCards() { return getCards(); }\n')
        stdout, _ = self.command("callers", "getCards", "--json")
        self.assertIn('"caller": "loadCards"', stdout)
        stdout, _ = self.command("imports", "controller.ts", "--json")
        self.assertIn('"resolvedPath": "service.ts"', stdout)


if __name__ == "__main__":
    unittest.main()
