# Recordings of the old console for console/routes/console

The files below are fixed records of what the Node console sent, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes an answer on
purpose, the test names the exception with the row id (as
`TestBootstrapUnderAPolicyAsNodeAnswered` does for a super admin, A92).

| File | What it holds | Replayed by |
|---|---|---|
| `bootscript.json` | what `GET /console-boot.js` sent for 102 sets of boot values | `TestBootScriptAsNodeSentIt` (`boot_test.go`) |
| `bootstrap_with_policy.json` | the access, navigation and permissions parts of `GET /api/console/bootstrap` under an account policy, for four roles | `TestBootstrapUnderAPolicyAsNodeAnswered` (`routes_test.go`) |

## What is recorded

`bootscript.json` came from `routes/console.routes.js` with stand-ins for
the services it reads. The boot values cover languages written left to
right and right to left, every theme mode, and values that must be escaped
(`</script>`, `&`, `>`, line and paragraph separators, quotes, control
characters, text outside the basic plane). The test builds the script from
the same boot object and compares it byte for byte.

`bootstrap_with_policy.json` was first taken from a contract recording.
The recorder saved the policy with `savePolicy` and resolved it with
`resolveForUser`, as the routes do, on an in-memory database, with the
built-in role map as a role's permissions; run on the tree before the
upstream 3.5.264 merge, it gave the same file.
