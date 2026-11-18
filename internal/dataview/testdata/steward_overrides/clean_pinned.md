# DATASET SUMMARY
Dataset: work_orders
Clean Rows: 3 | Bad Rows: 0
Page: 1 of 1 | Matching Rows: 3 | Showing: 3 | Steward-Pinned Cells: 1
Legend: [H] = Cell has history | [W<id>] = warning <id> flagged that cell, or the whole row in the Issues column | [S] = value pinned by a steward overwrite; what it displaced is under STEWARD OVERRIDES
Add --include-issues for the message behind each [A<id>]/[W<id>].
Reserved columns — warning_item_ids_ -> the [A<id>]/[W<id>] markers and the Issues column. Show one with --fields <name>; -o json has them all.

| id | District | Discount | Issues |
|---|---|---|---|
| 2001 | North | 10.00% | - |
| 2000 | East | 13.00% [H] [W55] [S] | [W55] discount |
| 1999 | South | 20.00% | - |

## STEWARD OVERRIDES
- 2000 discount: 12.50% → 13.00%
