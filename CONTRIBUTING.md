# Contributing to NeoDesk

Changes come in as pull requests against `main`. The guide for contributors
is on the website:

- [Contributing](https://neode.sk/docs/development/contributing): how to
  propose a change, what a pull request needs, commit messages and
  contributor licensing.
- [Building from source](https://neode.sk/docs/development/building) and
  [Tests and checks](https://neode.sk/docs/development/tests-and-checks):
  how to build the server with its web console and which checks to run
  before you open the pull request.
- [Architecture](https://neode.sk/docs/development/architecture): how the
  repository is laid out and how the parts fit together.
- [Translations](https://neode.sk/docs/development/translations): how to
  improve or add a language of the console.

Questions go to the [NeoDesk Discord](https://discord.gg/nwERSawufr) or to
[GitHub Discussions](https://github.com/neodeskapp/neodesk/discussions).
Report security problems privately, as [SECURITY.md](SECURITY.md) describes,
not on Discord or in Discussions.

## AI-assisted contributions

AI tools are part of how NeoDesk is developed, and you're welcome to use
them for code, tests, translations and documentation. You don't need to say
that you did. You are responsible for what you submit, the same as for
anything you wrote by hand: understand it, run it, and be ready to explain
it in review.

### Use a capable model

Use a current, capable model, the strongest one your tool offers. Don't use
small, heavily quantized or older-generation models. NeoDesk is a large
codebase built on the RustDesk protocol, which most models know little
about, and weaker models fill the gaps with guesses that look right:

- protocol messages, fields and ports that RustDesk clients don't send or
  expect
- settings, environment variables and API routes that don't exist in
  NeoDesk
- database changes that work on SQLite and break on PostgreSQL, or the
  other way round
- console strings added in one language instead of in every locale file
- tests that pass no matter what the code does

Read the whole diff before you open the pull request, and run the checks
from [Tests and checks](https://neode.sk/docs/development/tests-and-checks)
on it.

## License

NeoDesk is under [AGPL-3.0](LICENSE). Who keeps the copyright in a
contribution, and how outside work is credited, is in
[Contributor licensing](https://neode.sk/docs/development/contributing#contributor-licensing).
