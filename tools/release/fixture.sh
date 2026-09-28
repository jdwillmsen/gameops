# Shared by the release glue tests: builds a scratch history from this tree
# with every releasable component tagged at 1.0.0, then one commit per case,
# pushed to a local bare remote. Sets repo, sr and work, and leaves the shell
# in the clone.
repo="$(git rev-parse --show-toplevel)"
sr="$repo/tools/release/node_modules/.bin/semantic-release"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git init -q --bare -b main "$work/remote.git"
git clone -q "$work/remote.git" "$work/clone" 2>/dev/null
cd "$work/clone"
# semantic-release reads the CI's own branch from these even with --no-ci, so
# under GitHub Actions it would judge the PR ref, not the fixture's main.
unset CI $(compgen -e | grep '^GITHUB_')
export GIT_CONFIG_GLOBAL=/dev/null GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid \
  GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
# The working tree, not HEAD, so an uncommitted change to the glue is what
# gets tested.
(cd "$repo" && git ls-files -z -co --exclude-standard | tar --null -T - -c) | tar -x
ln -s "$repo/tools/release/node_modules" tools/release/node_modules
git add -A && git commit -qm "chore: fixture root"
for c in agent bridge afkbot; do git tag "$c-v1.0.0"; done

# A // line keeps go.mod parseable; the other files do not care.
change() { echo "// $2" >> "$1"; git add -A; git commit -qm "$2"; }
change minecraft/agent/README.md "feat: agent feature"
change minecraft/bridge/README.md "fix: bridge fix"
change README.md "feat: root-only change releases nothing"
change go.mod "chore(deps): bump a shared dependency"
git push -q origin main --tags
