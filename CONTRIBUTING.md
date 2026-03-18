# Decision rule for future features (use this to say “no”)

Ship in CLI only if all true:

- High frequency or high urgency;

- Safe to run non-interactively (clear idempotence and exit codes);

- Improves automation (can be composed in CI/SRE workflows);

- Doesn’t require rich human-in-the-loop UI.

Anything else belongs in the UI or SDK.
