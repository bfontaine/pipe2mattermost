# Pipe2Mattermost

**pipe2mattermost** is a simple CLI tool that pipes its stdin into
[Mattermost][].

[Mattermost]: https://about.mattermost.com/

## Setup

Put your Mattermost credentials under a `mattermost` entry in `~/.netrc`:

```netrc
machine mattermost
login yourlogin@company.com
password topsecret
```

If this is the first time you use this file make sure you’re the only one with
read/write access.

## Usage

    $ tail -f | pipe2mattermost [-update] <server URL> <channel slug>

If `-update` is passed it’ll continuously update the same message instead of
posting multiple ones.

Each line read is posted as a message. There’s no frequency limit so it’ll post
each line as soon as it reads it.
