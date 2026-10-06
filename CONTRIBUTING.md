# Contributing

See [Development](docs/DEVELOPMENT.md) for the build and release steps.

Use four spaces, braces around control flow, and one statement per line in C++. Run `scripts/format.ps1` before submitting a change. It applies clang-format to our C++ and gofmt to our Go code. Leave third-party source formatting alone.

Keep comments for things the code cannot explain, such as ROM layouts or audio-thread constraints. Keep the README aimed at musicians.

Run the build and tests. Explain the problem, what changes for the user, and how you checked it. Game samples and ROMs do not belong in pull requests; CI uses a generated test tone.

Contributions use the project's AGPL-3.0 licence. Keep existing third-party licence notices.
