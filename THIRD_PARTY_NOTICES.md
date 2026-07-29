# Third-Party Notices

## AtomCode

Atom2Api's OAuth and Coding Plan API integration follows the public source and wire contracts in:

- Project: AtomCode
- Repository: https://github.com/atomgit-atomcode/atomcode
- License: MIT
- Revision inspected: `e99a3ff2cb36b02ed7746d16c57403bdf2602c38`

## atomgit-opencode-bridge

The isolated `atomcode-signing-v1` implementation in `proxy.go` follows the independently documented algorithm in:

- Project: atomgit-opencode-bridge
- Repository: https://github.com/Small-tailqwq/atomgit-opencode-bridge
- License declaration: MIT (`package.json` and README)
- Revision inspected: `687b36135a5599e16b79c351e5da0bd9a2c4d73f`

The original project notes that it is unofficial and that users must comply with AtomCode and AtomGit terms. The same applies to Atom2Api. The signing primitive is intentionally isolated so it can be replaced if the upstream protocol or key changes.
