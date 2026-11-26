"""Migration and SQL predicate checks using SQLite from the standard library.

This validates schema constraints and representative SQL statements only; the
Go registry integration still needs a PostgreSQL-backed E2E test.
"""
import pathlib
import sqlite3
import unittest

MIGRATION = pathlib.Path(__file__).resolve().parents[2] / "packages/db/migrations/025_device_enrollment.sql"

class MigrationTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(":memory:")
        self.db.execute("PRAGMA foreign_keys = ON")
        self.db.execute("CREATE TABLE organizations (id UUID PRIMARY KEY, deleted_at TIMESTAMPTZ)")
        self.db.execute("CREATE TABLE users (id UUID PRIMARY KEY, organization_id UUID NOT NULL REFERENCES organizations(id), deleted_at TIMESTAMPTZ)")
        self.db.executemany("INSERT INTO organizations(id) VALUES(?)", [("orgA",), ("orgB",)])
        self.db.executemany("INSERT INTO users(id,organization_id) VALUES(?,?)", [("alice","orgA"),("bob","orgB")])
        up = MIGRATION.read_text().split("-- +goose Down", 1)[0]
        sql = "\n".join(line for line in up.splitlines() if not line.startswith("-- +goose"))
        self.db.executescript(sql)

    def tearDown(self):
        self.db.close()

    def test_unique_key_and_terminal_revocation(self):
        pub = b"a" * 32
        self.db.execute("INSERT INTO enrolled_devices(device_id,organization_id,owner_user_id,public_key) VALUES(?,?,?,?)", ("dp_key","orgA","alice",pub))
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute("INSERT INTO enrolled_devices(device_id,organization_id,owner_user_id,public_key) VALUES(?,?,?,?)", ("dp_key","orgB","bob",pub))
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute("INSERT INTO enrolled_devices(device_id,organization_id,owner_user_id,public_key) VALUES(?,?,?,?)", ("dp_bad","orgA","alice",b"short"))
        for owner,org in [("bob","orgB"),("bob","orgA")]:
            changed = self.db.execute("UPDATE enrolled_devices SET revoked_at=CURRENT_TIMESTAMP WHERE device_id=? AND organization_id=? AND owner_user_id=? AND revoked_at IS NULL",("dp_key",org,owner))
            self.assertEqual(changed.rowcount, 0)
        changed = self.db.execute("UPDATE enrolled_devices SET revoked_at=CURRENT_TIMESTAMP WHERE device_id=? AND organization_id=? AND owner_user_id=? AND revoked_at IS NULL",("dp_key","orgA","alice"))
        self.assertEqual(changed.rowcount, 1)
        changed = self.db.execute("UPDATE enrolled_devices SET revoked_at=CURRENT_TIMESTAMP WHERE device_id=? AND organization_id=? AND owner_user_id=? AND revoked_at IS NULL",("dp_key","orgA","alice"))
        self.assertEqual(changed.rowcount, 0)
        active = self.db.execute("SELECT public_key FROM enrolled_devices WHERE device_id=? AND revoked_at IS NULL", ("dp_key",)).fetchone()
        self.assertIsNone(active)

    def test_nonce_is_one_use_bound_to_owner_and_not_expired(self):
        self.db.execute("INSERT INTO device_enrollment_challenges(nonce,device_id,organization_id,user_id,public_key,issued_unix,expires_unix) VALUES(?,?,?,?,?,?,?)", ("nonce", "dp_key","orgA","alice",b"a"*32,1000,1060))
        query = "UPDATE device_enrollment_challenges SET consumed_unix=? WHERE nonce=? AND organization_id=? AND user_id=? AND consumed_unix IS NULL AND expires_unix>?"
        for org,user in [("orgB","bob"),("orgA","bob")]:
            self.assertEqual(self.db.execute(query,(1030,"nonce",org,user,1030)).rowcount,0)
        self.assertEqual(self.db.execute(query,(1060,"nonce","orgA","alice",1060)).rowcount,0)
        self.assertEqual(self.db.execute(query,(1030,"nonce","orgA","alice",1030)).rowcount,1)
        self.assertEqual(self.db.execute(query,(1030,"nonce","orgA","alice",1030)).rowcount,0)
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute("INSERT INTO device_enrollment_challenges(nonce,device_id,organization_id,user_id,public_key,issued_unix,expires_unix) VALUES(?,?,?,?,?,?,?)", ("nonce", "dp_other","orgB","bob",b"a"*32,1000,1060))
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute("INSERT INTO device_enrollment_challenges(nonce,device_id,organization_id,user_id,public_key,issued_unix,expires_unix) VALUES(?,?,?,?,?,?,?)", ("other", "dp_other","orgB","bob",b"a"*32,1000,1300))

if __name__ == "__main__":
    unittest.main()
