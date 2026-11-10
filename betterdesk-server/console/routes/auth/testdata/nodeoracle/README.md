# Node oracle for the sign-in page's capacity badge

`badge.js` loads the Node console's `services/serverAttestation.js` with a
stand-in for the settings table and computes, as `GET /api/auth/login-page`
did, the `attestation` of the sign-in page for 12,542 combinations of the
stored test result (`server_attestation_result`), the stored badge texts
(`server_attestation_badge_config`), the brand name and the texts of the
request language, and for a settings table that cannot be read. The result
goes to `../badge.json.gz`; `TestBadgeAsNodeMadeIt` replays every vector
through `badgeFor`.

The results are hostile on purpose: values of every JSON type, tiers named
like what every JavaScript object has, texts that need trimming and cutting
at their limits, JSON that cannot be read.

This is a fixture for the time both servers exist; the recorded file stays
when the Node code is gone.

    tools/dev.sh "cd betterdesk-server/console/routes/auth/testdata/nodeoracle && node badge.js"
