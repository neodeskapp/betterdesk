# Recordings of the old console for console/config

`settings.json` is a fixed record of what the Node console's
`config/config.js` made of many environments, made while it still existed.
It can't be recorded again. The Go test replays it and needs no Node.
Don't edit it: when a fix changes a setting on purpose, the test lists
that case as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `settings.json` | an environment per case and the settings `config/config.js` read from it | `TestSettingsAsTheOldConsoleReadThem` (`config_test.go`) |
