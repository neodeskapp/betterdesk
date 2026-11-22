# Recordings of the old console for console/billingclock

The JSON files here are fixed records of what the Node console did, made
while it still existed. They can't be recorded again. The Go tests replay
them and need no Node. Don't edit them: when a fix changes a result on
purpose, the test lists that record as an intended difference with the row
id.

| File | What it holds | Replayed by |
|---|---|---|
| `zones.json` | every time zone name `Intl.DateTimeFormat` of Node 22 took, found in the ICU data of the node binary, the zoneinfo folder and `Intl.supportedValuesOf`, with case variants, offsets and other text | `TestZoneVectors` |
| `hostzone.json` | the machine's time zone as Intl reported it and what `detectOsTimezone()` made of it, for 40 values of TZ and 9 targets of `/etc/localtime` | `TestHostZoneVectors` |
| `service.json` | `services/billingClockConfigService.js` called with many inputs, on Linux and on Windows; writes into the `.env` file; saves and reads outside and inside the Docker layout with two containers, with the API client stubbed | `TestServiceVectors`, `TestWriteVectors`, `TestSaveAndReadVectors` |

The zone table the package embeds, `../zones.txt`, was written from Node
in the same recording as `zones.json`. It can't be generated again either.
