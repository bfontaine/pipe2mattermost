# Pipe2Mattermost

**pipe2mattermost** is a simple CLI tool that pipes its stdin into [Mattermost][].

[Mattermost]: https://mattermost.com/

## Setup

Put your Mattermost credentials under a `mattermost` entry in `~/.netrc`:

```netrc
machine mattermost
login yourlogin@company.com
password topsecret
```

Warning: this file is plain text; use `chmod 0600` or similar to ensure you’re the only one who can access it.

## Usage

    $ tail -f my.log | pipe2mattermost [-update] <server URL> <channel slug>

If `-update` is passed it continuously updates the same message instead of posting multiple ones.

Each line read is posted as a message. There’s no frequency limit so it posts each line as soon as it reads it.
