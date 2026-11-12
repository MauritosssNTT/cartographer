#!/usr/bin/env bash
# Publishes the README's films on the orphan branch `readme-film` (D807): one commit holding every film's WebP,
# force-pushed, like the hero on `readme-assets` (D790) but on a branch of its own, so the hero workflow's
# force-push never drops it and the films never grow the history of main.
#
# The commit carries a `Film-Tree:` trailer, the hash of the films' inputs at HEAD (film/inputs.sh), which
# film-needed.sh compares to decide whether the films must be rendered again (D1068). One hash covers the whole set.
#
# Usage, from the repository root after a full render (film/README.md): film/publish.sh [file...]
# With no argument it publishes every film/out/*.webp present; each file goes into the tree under its own name
# (film/out/film.webp is film.webp, the launch film's URL).
# bash 3.2 compatible: it runs on macOS too.
set -euo pipefail
# shellcheck source=film/inputs.sh
. "$(dirname "${BASH_SOURCE[0]}")/inputs.sh"

FILES=("$@")
if [ "${#FILES[@]}" -eq 0 ]; then
    for f in film/out/*.webp; do
        if [ -e "$f" ]; then FILES+=("$f"); fi
    done
    [ "${#FILES[@]}" -gt 0 ] || { echo "publish: no film/out/*.webp, render the films first (film/README.md)" >&2; exit 1; }
fi

ENTRIES=""
SEEN=" "
for f in "${FILES[@]}"; do
    [ -s "$f" ] || { echo "publish: ${f} is missing or empty, render it first (film/README.md)" >&2; exit 1; }
    name="$(basename "$f")"
    case "$name" in
    *.webp) ;;
    *) echo "publish: ${f} is not a .webp" >&2; exit 1 ;;
    esac
    case "$SEEN" in
    *" ${name} "*) echo "publish: two files are named ${name}" >&2; exit 1 ;;
    esac
    SEEN="${SEEN}${name} "
    ENTRIES="${ENTRIES}$(printf '100644 blob %s\t%s\n' "$(git hash-object -w "$f")" "$name")
"
done
NOTE="$(printf '%s\n' \
    'The README films live here: written by film/publish.sh (D807) and force-pushed on every re-render, so this branch is always one commit. Do not commit to it.' \
    | git hash-object -w --stdin)"
ROOT="$(printf '100644 blob %s\tREADME.md\n%s' "$NOTE" "$ENTRIES" | git mktree)"
TREE="$(film_tree HEAD)"
COMMIT="$(printf 'film: rendered from %s\n\nFilm-Tree: %s\n' "$(git rev-parse --short HEAD)" "$TREE" | git commit-tree "$ROOT")"
git push --force origin "${COMMIT}:refs/heads/readme-film"
echo "publish: readme-film is ${COMMIT} (film tree ${TREE}; ${SEEN# })"
