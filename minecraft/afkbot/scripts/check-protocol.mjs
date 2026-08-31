// Run hourly by a workflow_dispatch from the jdw-deployments CronJob that
// keeps the production server on Mojang's LATEST release. Protocol numbers
// are far more stable than version strings — 1.26.40 and 1.26.43 both answer
// protocol 2168 — so a bare string diff would open a PR on every server
// patch release. This resolves both sides to protocol numbers via
// minecraft-data's own table before deciding anything changed.
import { readFileSync, writeFileSync } from 'node:fs'
import md from 'minecraft-data'

const productionVersion = process.argv[2]
if (!productionVersion) {
  console.error('usage: check-protocol.mjs <production-version>')
  process.exit(1)
}

const githubOutput = process.env.GITHUB_OUTPUT
function setOutput(key, value) {
  if (githubOutput) writeFileSync(githubOutput, `${key}=${value}\n`, { flag: 'a' })
}

function noop(reason) {
  console.log(reason)
  setOutput('changed', 'false')
  process.exit(0)
}

const dockerfilePath = 'Dockerfile'
const dockerfile = readFileSync(dockerfilePath, 'utf8')

// The Dockerfile bakes MC_VERSION twice (builder + runtime stages) and both
// must always agree — treat disagreement as a repo bug, not a no-op.
const argMatches = [...dockerfile.matchAll(/ARG MC_VERSION=(\S+)/g)]
if (argMatches.length !== 2) {
  console.error(`expected 2 "ARG MC_VERSION=" lines in Dockerfile, found ${argMatches.length}`)
  process.exit(1)
}
const [bakedVersion, otherBakedVersion] = argMatches.map((m) => m[1])
if (bakedVersion !== otherBakedVersion) {
  console.error(`Dockerfile's two ARG MC_VERSION lines disagree: ${bakedVersion} vs ${otherBakedVersion}`)
  process.exit(1)
}

const versions = md.versions.bedrock

const bakedEntry = versions.find((e) => e.minecraftVersion === bakedVersion)
if (!bakedEntry) {
  // Our own baked version not resolving is a repo bug (or a minecraft-data
  // downgrade), never the expected no-op path — fail loudly.
  console.error(`baked MC_VERSION ${bakedVersion} has no minecraft-data table entry — Dockerfile is misconfigured`)
  process.exit(1)
}

const productionEntry = versions.find((e) => e.minecraftVersion === productionVersion)
if (!productionEntry) {
  noop(
    `production version ${productionVersion} has no minecraft-data table entry yet — upstream (bedrock-protocol/minecraft-data) doesn't support it. Nothing to do.`,
  )
}

if (productionEntry.version === bakedEntry.version) {
  noop(
    `already current: production ${productionVersion} and baked ${bakedVersion} both resolve to protocol ${bakedEntry.version}`,
  )
}

// A table entry existing is not the same guarantee as minecraft-data bundling
// full block/item data for that exact version string — the same check the
// Dockerfile itself runs post-prune.
let fullData
try {
  fullData = md(`bedrock_${productionVersion}`)
} catch {
  fullData = null
}
if (!fullData || !fullData.blocks || !fullData.items) {
  noop(
    `production version ${productionVersion} resolves to protocol ${productionEntry.version} but minecraft-data lacks full block/item data for it yet. Nothing to do.`,
  )
}

console.log(
  `protocol bump detected: ${bakedVersion} (protocol ${bakedEntry.version}) -> ${productionVersion} (protocol ${productionEntry.version})`,
)

const updatedDockerfile = dockerfile.replaceAll(`ARG MC_VERSION=${bakedVersion}`, `ARG MC_VERSION=${productionVersion}`)
writeFileSync(dockerfilePath, updatedDockerfile)

const packageJsonPath = 'package.json'
const pkg = JSON.parse(readFileSync(packageJsonPath, 'utf8'))
const [majorStr, minorStr, patchStr] = pkg.version.split('.')
pkg.version = `${majorStr}.${minorStr}.${Number(patchStr) + 1}`
writeFileSync(packageJsonPath, `${JSON.stringify(pkg, null, 2)}\n`)

setOutput('changed', 'true')
setOutput('old_version', bakedVersion)
setOutput('new_version', productionVersion)
setOutput('old_protocol', String(bakedEntry.version))
setOutput('new_protocol', String(productionEntry.version))
setOutput('new_package_version', pkg.version)
