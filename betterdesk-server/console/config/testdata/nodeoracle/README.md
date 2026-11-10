# Node oracle for the settings

`settings.js` loads the Node console's `config/config.js` with many
environments and writes what it makes of them to `../settings.json`. The
Go tests replay the file (`TestSettingsAsTheOldConsoleReadThem`).

It is a fixture: it goes away with the Node console. Run it in the dev
container:

    tools/dev.sh "cd betterdesk-server/console/config/testdata/nodeoracle && node settings.js"
