No version bump needed (backwards-compatible changes):
- Adding a new optional field (existing manifests still work)
- Adding new enum values
- Making a required field optional
- Adding new endpoints

Version bump needed (breaking changes):
- Adding a new required field (old manifests would fail validation)
- Removing a field
- Renaming a field
- Changing a field's type (e.g., string → int)
- Changing field semantics (same name, different meaning)

Example:

# v1 manifest - still valid after adding optional "ssl_mode" field
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: my-db
spec:
  type: postgresql
  host: localhost
  port: 5432
  # ssl_mode: prefer  ← new optional field, can be omitted

If you later need a breaking change, you'd create qluster.ai/v2 and support both
 versions during a migration period.

So the answer: If the API adds a new optional field, your manifest version stays
 v1. The CLI just needs to be updated to recognize and pass the new field to the
 API.
