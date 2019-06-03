# go-ipfs-log

> An append-only log on IPFS.

`ipfs-log` is an immutable, operation-based conflict-free replicated data structure ([CRDT](https://en.wikipedia.org/wiki/Conflict-free_replicated_data_type)) for distributed systems. It's an append-only log that can be used to model a mutable, shared state between peers in p2p applications.

Every entry in the log is saved in IPFS and each points to a hash of previous entry(ies) forming a graph. Logs can be forked and joined back together.

```
           Log A                Log B
             |                    |
     logA.append("one")   logB.append("hello")
             |                    |
             v                    v
          +-----+             +-------+
          |"one"|             |"hello"|
          +-----+             +-------+
             |                    |
     logA.append("two")   logB.append("world")
             |                    |
             v                    v
       +-----------+       +---------------+
       |"one","two"|       |"hello","world"|
       +-----------+       +---------------+
             |                    |
             |                    |
       logA.join(logB) <----------+
             |
             v
+---------------------------+
|"one","hello","two","world"|
+---------------------------+
```


## Table of Contents

- [Background](#background)
- [Install](#install)
- [Usage](#usage)
- [API](#api)
- [Tests](#tests)
- [Contribute](#contribute)
- [License](#license)

## Background

IPFS Log has a few use cases:

- CRDTs
- Database operations log
- Feed of data
- Track a version of a file
- Messaging

[ipfs-log](https://github.com/orbitdb/ipfs-log/) was originally created for [orbit-db](https://github.com/orbitdb/orbit-db) - a distributed peer-to-peer database on [IPFS](https://github.com/ipfs/ipfs). This library intends to provide a fully compatible port of the JavaScript version in Go.

## Install

This project uses [go](https://golang.org/).

```
go get github.com/berty/go-ipfs-log
```

## Usage

See the [API documentation](#api) for more details.

### Quick Start

Install dependencies:

```go
// TODO
```

Run a simple program:

```go
// TODO
```

## API

See [API Documentation](https://github.com/orbitdb/ipfs-log/tree/master/API.md) for full details.

- [Log](https://github.com/orbitdb/ipfs-log/tree/master/API.md#log)
  - [Constructor](https://github.com/orbitdb/ipfs-log/tree/master/API.md##constructor)
    - [new Log(ipfs, identity, [{ logId, access, entries, heads, clock, sortFn }])](https://github.com/orbitdb/ipfs-log/tree/master/API.md##new-log-ipfs-id)
  - [Properties](https://github.com/orbitdb/ipfs-log/tree/master/API.md##properties)
    - [id](https://github.com/orbitdb/ipfs-log/tree/master/API.md##id)
    - [values](https://github.com/orbitdb/ipfs-log/tree/master/API.md##values)
    - [length](https://github.com/orbitdb/ipfs-log/tree/master/API.md##length)
    - [clock](https://github.com/orbitdb/ipfs-log/tree/master/API.md##length)
    - [heads](https://github.com/orbitdb/ipfs-log/tree/master/API.md##heads)
    - [tails](https://github.com/orbitdb/ipfs-log/tree/master/API.md##tails)
  - [Methods](https://github.com/orbitdb/ipfs-log/tree/master/API.md##methods)
    - [append(data)](https://github.com/orbitdb/ipfs-log/tree/master/API.md##appenddata)
    - [join(log)](https://github.com/orbitdb/ipfs-log/tree/master/API.md##joinlog)
    - [toMultihash()](https://github.com/orbitdb/ipfs-log/tree/master/API.md##tomultihash)
    - [toBuffer()](https://github.com/orbitdb/ipfs-log/tree/master/API.md##tobuffer)
    - [toString()](https://github.com/orbitdb/ipfs-log/tree/master/API.md##toString)
  - [Static Methods](https://github.com/orbitdb/ipfs-log/tree/master/API.md##static-methods)
    - [Log.fromEntry()]()
    - [Log.fromEntryCid()]()
    - [Log.fromCID()]()
    - [Log.fromMultihash()]()

## Tests

Run all tests:
```
cd test && go test
```

## Contribute

If you find a bug or something is broken, let us know! PRs and [issues](https://github.com/berty/go-ipfs-log/issues) are gladly accepted too. Take a look at the open issues, too, to see if there is anything that you could do or someone else has already done. Here are some things I know I need:

### TODO

- Ensure 1-1 compatibility with JS version


## License

[MIT](LICENSE) © 2019 Berty Technologies
