# The Node console as the reference for `console/billingclock`

This folder is a test fixture. It goes away together with `web-nodejs`;
the files it wrote stay as the record of what the Node console did.

| Script | Writes | Replayed by |
|---|---|---|
| `zones.js` | `../../zones.txt` (the zone table the package embeds) and `../zones.json`: every time zone name Intl.DateTimeFormat of Node 22 takes, found in the ICU data of the node binary, the zoneinfo folder and Intl.supportedValuesOf, with case variants, offsets and other text | `TestZoneVectors` |
| `hostzone.js` | `../hostzone.json`: the machine's time zone as Intl reported it and what `detectOsTimezone()` made of it, for 40 values of TZ and 9 targets of `/etc/localtime` (it changes the link, so it runs as root in a throwaway container) | `TestHostZoneVectors` |
| `service.js` | `../service.json`: `services/billingClockConfigService.js` called with many inputs, on Linux and on Windows; writes into the `.env` file; saves and reads outside and inside the Docker layout with two containers, with the API client stubbed | `TestServiceVectors`, `TestWriteVectors`, `TestSaveAndReadVectors` |

The scripts load the real service from `/repo/web-nodejs`. It was
deleted when the routes moved to Go; to run them again, put the old file
back in a copy of the console (`goapipath/testdata/nodeoracle/overlay.sh`
does that for route files; the service comes from commit dd2da222):

    git -C <worktree> show dd2da222:web-nodejs/services/billingClockConfigService.js > /tmp/oracle/web-nodejs/services/billingClockConfigService.js
    tools/dev.sh "cd betterdesk-server/console/billingclock/testdata/nodeoracle && BD_REPO=/tmp/oracle node service.js"

`zones.js` and `hostzone.js` need only node (hostzone.js also the service).
