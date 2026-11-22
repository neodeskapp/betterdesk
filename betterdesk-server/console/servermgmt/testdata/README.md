# Recordings of the old console for console/servermgmt

The files below are fixed records of what the Node console's server
management did on the dev image, made while the console still existed.
They can't be recorded again. The Go tests replay them and need no Node.
Don't edit them: when a fix changes a result on purpose, the step goes
into the replay's list of intended differences (`fileChanges`,
`crlfServices`, or the note next to the terminal comparison) with the row
id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_files.json` | `services/serverManagement.js`: the folder lists under 8 settings of the environment, and 256 calls of the folder lists, list, read, write, mkdir, rename and delete in 13 scenarios on a tree with links into and out of the folders, a dangling link, a loop, a FIFO, sparse files of 8 MiB, broken UTF-8 and names that need care; after each change the whole tree | `TestNodeFileVectors` |
| `node_commands.json` | the same service with `child_process` replaced: disks from df and wmic, swap from `/proc/meminfo`, the service list from systemctl and PowerShell, and 2,076 calls of the service control on Linux, Windows and another system (refusals, command line, time limit, kill, result); also the server part of `GET /api/server-management/info` for 45 health answers of the API server | `TestNodeDisksAndSwap`, `TestNodeServiceList`, `TestNodeServiceControl`, and `TestNodeHealthVectors` of `routes/servermanagement` |
| `node_terminal_pipe.json`, `node_terminal_pty.json` | `services/serverTerminalProxy.js` on a real HTTP server, with node-pty hidden (pipe mode) and with it: 14 raw upgrades (origin, session, permission, handshake) and 37 conversations (init, input, resize, close, exit codes, signals, a second init, a binary message, a character split across reads, 100 kB of output, terminal sizes, the account the shell runs as), with the audit entries and permission lookups | `TestNodeTerminalPipe`, `TestNodeTerminalPty` |

## What is recorded

The file tree was built in `/opt/smoracle`, outside every folder the file
browser always may read. The Go test builds the same tree from the
recording in a temp folder and maps the fixed folders (`/var/log`, `/tmp`,
`/var/tmp`) to folders of its own. A change guard in the Go test stops any
change outside its folder, so a broken check cannot reach the real file
system.

The terminal was recorded as root, so the shell switched to the image's
account `node`; the Go terminal tests that replay it run only as root where
that account exists. Where the Go terminal differs on purpose, the test
says so next to the comparison: a shell asked to stop is gone afterwards
(pipe mode in Node left an interactive bash running), a shell that
switched accounts has no supplementary groups (node-pty kept root's group
0), and pipe output is decoded across reads (SM10).
