// Exercise the same Release Please engine bundled in the pinned action, offline.
// Usage: node tests/release-policy.cjs <directory containing node_modules>
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const modules = path.resolve(process.argv[2], 'node_modules');
const rp = require(path.join(modules, 'release-please'));
const Ajv = require(path.join(modules, 'ajv'));
const {Go} = require(path.join(modules, 'release-please/build/src/strategies/go'));
const {DefaultVersioningStrategy} = require(path.join(modules, 'release-please/build/src/versioning-strategies/default'));
const {parseConventionalCommits} = require(path.join(modules, 'release-please/build/src/commit'));
const {Version} = require(path.join(modules, 'release-please/build/src/version'));
const {TagName} = require(path.join(modules, 'release-please/build/src/util/tag-name'));
const config = JSON.parse(fs.readFileSync('release-please-config.json', 'utf8'));
const manifest = JSON.parse(fs.readFileSync('.release-please-manifest.json', 'utf8'));
const ajv = new Ajv({strict: false, logger: false});
assert(ajv.validate(rp.configSchema, config), JSON.stringify(ajv.errors));
assert(ajv.validate(rp.manifestSchema, manifest), JSON.stringify(ajv.errors));
assert.equal(rp.VERSION, '17.6.0', 'Keep tests aligned with the action bundle');
const options = config.packages['.'];
assert.equal(options['release-type'], 'go');
assert.equal(options.draft, true);
assert.equal(options['force-tag-creation'], true);
assert.equal(options['release-as'], undefined, 'Do not permanently override the next version');
const logger = Object.fromEntries(['info', 'warn', 'debug', 'error'].map(key => [key, () => {}]));
const strategy = new Go({
  github: {repository: {owner: 'hkgdevx', repo: 'hosted-github-runner'}},
  targetBranch: 'main',
  initialVersion: options['initial-version'],
  logger,
  includeComponentInTag: options['include-component-in-tag'],
  includeVInTag: options['include-v-in-tag'],
  changelogSections: options['changelog-sections'],
  pullRequestFooter: options['pull-request-footer'],
  versioningStrategy: new DefaultVersioningStrategy({
    bumpMinorPreMajor: options['bump-minor-pre-major'],
    bumpPatchForMinorPreMajor: options['bump-patch-for-minor-pre-major'],
    logger,
  }),
});
const latest = {tag: new TagName(Version.parse('0.1.0')), sha: 'a'.repeat(40), notes: ''};
async function candidate(messages, previous = latest) {
  const commits = messages.map((message, i) => ({sha: String(i + 1).padStart(40, '0'), message, files: ['cmd/ghrctl/main.go']}));
  return strategy.buildReleasePullRequest(parseConventionalCommits(commits), previous);
}
async function main() {
  const initial = await candidate(['feat: automate releases\n\nRelease-As: 0.1.0'], null);
  assert.equal(initial.version.toString(), '0.1.0');
  assert.match(initial.body.toString(), /## Release review/);
  const {PullRequestBody} = require(path.join(modules, 'release-please/build/src/util/pull-request-body'));
  assert.equal(PullRequestBody.parse(initial.body.toString().replaceAll('- [ ]', '- [x]')).releaseData[0].version.toString(), '0.1.0');
  assert.equal((await candidate(['feat: automate releases'], null)).version.toString(), '0.1.0');
  for (const [message, expected] of [
    ['fix: handle restart', '0.1.1'],
    ['fix(deps): update shipped scanner', '0.1.1'],
    ['perf: reduce startup time', '0.1.1'],
    ['feat: add command', '0.2.0'],
    ['feat!: replace config\n\nBREAKING CHANGE: migrate config before upgrading', '0.2.0'],
    ['refactor!: change config layout', '0.2.0'],
  ]) {
    const pr = await candidate([message]);
    assert.equal(pr.version.toString(), expected, message);
    assert.equal(pr.headRefName, initial.headRefName, 'Use one rolling branch');
    if (message.includes('migrate config')) assert.match(pr.body.toString(), /migrate config/);
  }
  for (const message of ['docs: explain setup', 'ci: tune checks', 'chore(deps): update linter', 'test: add coverage', 'refactor: simplify code']) {
    assert.equal(await candidate([message]), undefined, message);
  }
  const batch = await candidate(['fix: handle restart', 'feat: add command', 'docs: update guide']);
  assert.equal(batch.version.toString(), '0.2.0');
  assert.equal(batch.headRefName, initial.headRefName);
  assert.match(batch.body.toString(), /handle restart/);
  assert.match(batch.body.toString(), /add command/);
  assert.doesNotMatch(batch.body.toString(), /update guide/);
  assert.equal(new TagName(batch.version, undefined, undefined, options['include-v-in-tag']).toString(), 'v0.2.0');
  console.log('Release Please schema, bootstrap, version policy, notes and rolling branch checks passed.');
}
main().catch(error => { console.error(error); process.exitCode = 1; });
