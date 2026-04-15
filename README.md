# rr

Batch rename files and directories.

Help:

```sh
rr --help
rr --help-long
```

Important behavior:

- `rr` builds the full rename plan first and checks for conflicts before changing anything
- transform flags run in the order you pass them
- short boolean flags can be bundled, for example `-lru`
- long flags must use `--long-form`
- `-n`, `--dry-run` shows the plan without changing files
- `-i`, `--interactive` opens `$EDITOR`, lets you edit the plan, then asks whether to apply it
- recursive numbering is rejected; for numbered renames, pass the items directly instead of `-r`, for example `rr -N 001_ somedir/*.png`
- numbered prefixes use the shape you pass: `-N 001_` means width `3`, start `1`, separator `_`
- case-insensitive filesystems are handled, including case-only renames like `Foo` -> `foo`

Main flags:

- `-e`, `--sub <expr>`: regex substitution, repeatable
- `-u`, `--underscores`: replace whitespace runs with underscores
- `-l`, `--lower`: lowercase basenames
- `-f`, `--files-only`: rename files only
- `-d`, `--dirs-only`: rename directories only
- `-N`, `--number-prefix <prefix>`: prefix with incrementing numbers like `001_` or `01`
- `-i`, `--interactive`: edit the planned renames before applying
- `-r`, `--recursive`: walk directories recursively
- `-n`, `--dry-run`: print the plan only
- `--color auto|always|never`: control colored output

Examples:

```sh
rr -n -l -u ~/music/*
rr -e 's/^/archive_/' *
rr -e 's/(\.[^.]+)$/_edited\1/' *.jpg
rr -f -N 001_ *.jpg
rr -rf -e 's/^IMG_//' -e 's/\.[Jj][Pp][Ee]?[Gg]$/.jpg/' ~/photos
rr -r -u -e 's/__+/_/g' ./incoming
rr -e 's/^([^,]+), (.+)$/\2 \1/' *
```

Build and install:

```sh
make build
make install
```

Binary:

```sh
./build/bin/host/rr
```
