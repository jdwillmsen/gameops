// Each component runs semantic-release on its own, but semantic-release reads
// every commit on main. This wraps the conventional-commits analyzer and
// notes generator so each run only sees commits that affect its component,
// as decided by tools/components -- the same code CI uses.
import { execFileSync } from 'node:child_process';
import * as analyzer from '@semantic-release/commit-analyzer';
import * as notes from '@semantic-release/release-notes-generator';

function componentCommits(context) {
  const component = context.env.RELEASE_COMPONENT;
  if (!component) {
    throw new Error('RELEASE_COMPONENT is not set; run through the release workflow');
  }
  const out = execFileSync(
    'go',
    ['run', './tools/components', 'commits', '-component', component],
    { cwd: context.cwd, input: context.commits.map((c) => c.hash).join('\n'), encoding: 'utf8' },
  );
  const kept = new Set(out.split('\n').filter(Boolean));
  context.logger.log('%s: %d of %d commits affect it', component, kept.size, context.commits.length);
  return { ...context, commits: context.commits.filter((c) => kept.has(c.hash)) };
}

export const analyzeCommits = (config, context) =>
  analyzer.analyzeCommits(config, componentCommits(context));

export const generateNotes = (config, context) =>
  notes.generateNotes(config, componentCommits(context));
