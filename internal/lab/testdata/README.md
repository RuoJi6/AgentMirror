# Python v2 compatibility fixtures

`python-v2.sql` and `python-v2-expected.json` were generated with the retained Python `backend.store.Store`, using a temporary database and synthetic records only.

The sample includes the original schema and defaults, three legacy demo deployments, a v1 session with a receipt and annotation, and a later v2 profile. `TestPythonDatabaseCompatibility` imports the SQL into a fresh SQLite file and compares the Go API output to the Python output. These demo deployments are test fixtures and are never seeded into new production databases.

The fixture contains no user database records, real credentials or collected environment information. The callback token belongs only to this synthetic test session. Go tests consume these static files directly and do not invoke Python.
