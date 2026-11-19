# Node oracle for console/servermgmt

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

Three scripts run the old console's server management on the dev image and
record what it did. The Go tests replay every recording.

| Script | Recording | What | Go test |
|---|---|---|---|
| `files.js` | `../node_files.json` | `services/serverManagement.js`: the folder lists under 8 settings of the environment, and 256 calls of the folder lists, list, read, write, mkdir, rename and delete in 13 scenarios on a tree with links into and out of the folders, a dangling link, a loop, a FIFO, sparse files of 8 MiB, broken UTF-8 and names that need care; after each change the whole tree | `TestNodeFileVectors` |
| `commands.js` | `../node_commands.json` | the same service with `child_process` replaced: disks from df and wmic, swap from `/proc/meminfo`, the service list from systemctl and PowerShell, and 2,076 calls of the service control on Linux, Windows and another system (refusals, command line, time limit, kill, result); also the server part of `GET /api/server-management/info` for 45 health answers of the API server | `TestNodeDisksAndSwap`, `TestNodeServiceList`, `TestNodeServiceControl`, `routes/servermanagement.TestNodeHealthVectors` |
| `terminal.js pipe`, `terminal.js pty` | `../node_terminal_pipe.json`, `../node_terminal_pty.json` | `services/serverTerminalProxy.js` on a real HTTP server, with node-pty hidden (pipe mode) and with it: 14 raw upgrades (origin, session, permission, handshake) and 37 conversations (init, input, resize, close, exit codes, signals, a second init, a binary message, a character split across reads, 100 kB of output, terminal sizes, the account the shell runs as), with the audit entries and permission lookups | `TestNodeTerminalPipe`, `TestNodeTerminalPty` |

`files.js` builds its tree in `/opt/smoracle`, outside every folder the
file browser always may read; the Go test builds the same tree from the
recording in a temp folder and maps the fixed folders (`/var/log`, `/tmp`,
`/var/tmp`) to folders of its own. A change guard in the Go test stops any
change outside its folder, so a broken check cannot reach the real file
system.

`terminal.js` ran as root, so the shell switched to the image's account
`node`; the Go terminal tests that replay it run only as root where that
account exists. Where the Go terminal differs on purpose, the test says so
next to the comparison: a shell asked to stop is gone afterwards (pipe mode
in Node left an interactive bash running), a shell that switched accounts
has no supplementary groups (node-pty kept root's group 0), and pipe output
is decoded across reads (SM10). The other replays list their intended
differences the same way (`fileChanges`, `crlfServices`).

These scripts are fixtures from the time both servers existed. The services
are gone from `web-nodejs` since they moved, so the scripts run only on a
tree that still has them (the commit before the move); the recordings stay.

    tools/dev.sh "cd betterdesk-server/console/servermgmt/testdata/nodeoracle && node files.js && node commands.js && node terminal.js pipe && node terminal.js pty"
