# Recordings of the old console for console/servermgmt

The files below are fixed records of what the Node console's server
management did on the dev image, made while the console still existed.
They can't be recorded again. The Go tests replay them and need no Node.
Don't edit them: when a fix changes a result on purpose, the step goes
into the replay's list of intended differences (`fileChanges` or
`crlfServices`) with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `node_files.json` | `services/serverManagement.js`: the folder lists under 8 settings of the environment, and 256 calls of the folder lists, list, read, write, mkdir, rename and delete in 13 scenarios on a tree with links into and out of the folders, a dangling link, a loop, a FIFO, sparse files of 8 MiB, broken UTF-8 and names that need care; after each change the whole tree | `TestNodeFileVectors` |
| `node_commands.json` | the same service with `child_process` replaced: disks from df and wmic, swap from `/proc/meminfo`, the service list from systemctl and PowerShell, and 2,076 calls of the service control on Linux, Windows and another system (refusals, command line, time limit, kill, result); also the server part of `GET /api/server-management/info` for 45 health answers of the API server | `TestNodeDisksAndSwap`, `TestNodeServiceList`, `TestNodeServiceControl`, and `TestNodeHealthVectors` of `routes/servermanagement` |

## What is recorded

The file tree was built in `/opt/smoracle`, outside every folder the file
browser always may read. The Go test builds the same tree from the
recording in a temp folder and maps the fixed folders (`/var/log`, `/tmp`,
`/var/tmp`) to folders of its own. A change guard in the Go test stops any
change outside its folder, so a broken check cannot reach the real file
system.
