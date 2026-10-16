"""Read-only inspection of the legacy SQLite database (source of the import)."""

import sqlite3
import sys

db_path = sys.argv[1]

con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
cur = con.cursor()

objects = cur.execute(
    "SELECT name, type FROM sqlite_master WHERE type IN ('table','view') ORDER BY name"
).fetchall()

print("=== legacy sqlite objects ===")
for name, kind in objects:
    print(f"{name:32} {kind}")

print("=== row counts ===")
for name, kind in objects:
    if kind != "table" or name.startswith("sqlite_"):
        continue
    try:
        count = cur.execute(f'SELECT COUNT(*) FROM "{name}"').fetchone()[0]
    except sqlite3.Error as exc:
        count = f"error: {exc}"
    print(f"{name:32} {count}")

con.close()
