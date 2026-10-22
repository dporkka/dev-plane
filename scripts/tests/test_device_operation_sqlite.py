"""SQLite contract tests for staged device operations; PostgreSQL E2E pending."""
from pathlib import Path
import re
import sqlite3
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / "apps/api/internal/devices/operations.go").read_text()
QUERIES = re.findall(r"\x60([^\x60]+)\x60", SOURCE, re.DOTALL)
def statement(token):
    found = [sql for sql in QUERIES if token in sql]
    if len(found) != 1:
        raise AssertionError((token, len(found)))
    return found[0]

RESERVE = statement("INSERT INTO device_operations")
DISPATCH = statement("UPDATE device_operations SET state='dispatched'")
UNKNOWN = statement("UPDATE device_operations SET state='unknown'")
RECEIPT = statement("UPDATE device_operations SET state=$7")

class LedgerTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(":memory:")
        self.db.execute("PRAGMA foreign_keys=ON")
        self.db.executescript("""
        CREATE TABLE organizations(id TEXT PRIMARY KEY,deleted_at TIMESTAMPTZ);
        CREATE TABLE users(id TEXT PRIMARY KEY,organization_id TEXT REFERENCES organizations(id),deleted_at TIMESTAMPTZ);
        CREATE TABLE projects(id TEXT PRIMARY KEY,organization_id TEXT REFERENCES organizations(id),deleted_at TIMESTAMPTZ);
        CREATE TABLE repositories(id TEXT PRIMARY KEY,project_id TEXT REFERENCES projects(id),deleted_at TIMESTAMPTZ);
        CREATE TABLE workspaces(id TEXT PRIMARY KEY,repository_id TEXT REFERENCES repositories(id),deleted_at TIMESTAMPTZ);
        INSERT INTO organizations(id) VALUES('org1'),('org2');
        INSERT INTO users(id,organization_id) VALUES('alice','org1'),('bob','org2');
        INSERT INTO projects(id,organization_id) VALUES('project1','org1'),('project2','org2');
        INSERT INTO repositories(id,project_id) VALUES('repo1','project1'),('repo2','project2');
        INSERT INTO workspaces(id,repository_id) VALUES('ws1','repo1'),('ws2','repo2');
        """)
        for version in (25,26):
            paths = list((ROOT/"packages/db/migrations").glob(f"{version:03d}_*.sql"))
            self.assertEqual(len(paths),1)
            up = paths[0].read_text().split("-- +goose Down")[0]
            self.db.executescript("\n".join(line for line in up.splitlines() if not line.startswith("-- +goose")))
        for device,org,user,key in [("device1","org1","alice",b"a"*32),("device2","org2","bob",b"b"*32)]:
            self.db.execute("INSERT INTO enrolled_devices(device_id,organization_id,owner_user_id,public_key) VALUES(?,?,?,?)",(device,org,user,key))
        self.id="dp1_"+"a"*64

    def tearDown(self): self.db.close()
    def execute(self, query, args):
        return self.db.execute(query,{str(i+1):value for i,value in enumerate(args)})
    def reserve(self, op=None,org="org1",user="alice",device="device1",ws="ws1",tool="files_read"):
        return self.execute(RESERVE,(op or self.id,org,user,device,ws,tool,"d"*64,1)).rowcount

    def test_scope_and_idempotency(self):
        self.assertEqual(self.reserve(),1)
        self.assertEqual(self.reserve(),0)
        other="dp1_"+"b"*64
        for override in [{"org":"org2"},{"user":"bob"},{"ws":"ws2"},{"device":"device2"}]:
            self.assertEqual(self.reserve(op=other,**override),0)
        self.assertEqual(self.reserve(op=other,org="org2",user="bob",device="device2",ws="ws2"),1)

    def test_dispatch_once_unknown_and_late_receipt(self):
        self.assertEqual(self.reserve(),1)
        args=(self.id,"org1","alice","device1","ws1",1)
        self.assertEqual(self.execute(DISPATCH,args).rowcount,1)
        self.assertEqual(self.execute(DISPATCH,args).rowcount,0)
        self.assertEqual(self.execute(UNKNOWN,args).rowcount,1)
        self.assertEqual(self.execute(DISPATCH,args).rowcount,0)
        self.assertEqual(self.execute(RECEIPT,args+("completed","e"*64)).rowcount,1)
        self.assertEqual(self.execute(RECEIPT,args+("failed","f"*64)).rowcount,0)

    def test_revocation_and_epoch_fencing(self):
        self.assertEqual(self.reserve(),1)
        args=(self.id,"org1","alice","device1","ws1",1)
        self.assertEqual(self.execute(DISPATCH,args[:-1]+(2,)).rowcount,0)
        self.db.execute("UPDATE enrolled_devices SET revoked_at=CURRENT_TIMESTAMP WHERE device_id='device1'")
        self.assertEqual(self.execute(DISPATCH,args).rowcount,0)
        self.assertEqual(self.reserve(op="dp1_"+"c"*64),0)

    def test_schema_excludes_shell_and_receiptless_completion(self):
        with self.assertRaises(sqlite3.IntegrityError):
            self.reserve(tool="shell")
        self.assertEqual(self.reserve(),1)
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute("UPDATE device_operations SET state='completed' WHERE operation_id=?",(self.id,))
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute("UPDATE device_operations SET owner_epoch=0 WHERE operation_id=?",(self.id,))
if __name__ == "__main__": unittest.main()
