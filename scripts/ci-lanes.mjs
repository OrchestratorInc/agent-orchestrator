import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const FRONTEND = /^(frontend\/(?!src\/docs\/|native\/)|packages\/(product-ui|shared|cloud-client)\/)/;

export const LANES = {
  go: [/^backend\//, /^go\.work(\.sum)?$/, /^contracts\/session-start-interface\.json$/, /^frontend\/scripts\/prepare-cloudflared\.mjs$/, /^frontend\/src\/api\/schema\.ts$/, /^package(-lock)?\.json$/, /^\.github\/scripts\/go-test-parallel\.sh$/],
  'cli-e2e': [/^backend\//, /^test\/cli\//],
  frontend: [FRONTEND],
  docs: [/^frontend\/src\/docs\//],
  boundary: [FRONTEND, /^backend\/internal\/httpd\//],
  cloud: [/^packages\/cloud-client\//, /^contracts\/cloud\//],
  'product-ui': [/^packages\/product-ui\//],
  mobile: [/^packages\/(mobile|shared)\//],
  'mac-native': [/^frontend\/native\//, /^frontend\/scripts\/build-(mac|update|wake|dictation)/, /^frontend\/assets\/(icon\.icns$|dmg\/)/],
  config: [/^\.github\//, /^scripts\//],
};

export const FRONTEND_WORKFLOW_LANES = ['frontend', 'docs', 'boundary', 'cloud', 'product-ui', 'mac-native'];

const EVERY_LANE = /^(\.github\/(workflows|actions)\/|scripts\/ci-lanes\.mjs$)/;
const ALL_VITEST = /^(frontend\/(package(-lock)?\.json|vite[^/]*\.ts|vitest[^/]*\.ts|tsconfig[^/]*\.json|src\/renderer\/test\/|scripts\/)|packages\/(product-ui|shared)\/)/;
const FILE_LIMIT = 3000;

export function pickLanes({ event, draft = false, base = 'main', fork = false, labels = [], files = [] }) {
  if (event === 'pull_request' && !draft && base !== 'main' && fork) {
    throw new Error(`This fork PR targets ${base}. CI cannot re-run it when it moves to main, so ci-ok stays red: target main and push again.`);
  }
  if (event === 'pull_request' && (draft || base !== 'main')) {
    return { lanes: [], full: false, macos: false, vitest: '' };
  }
  const full = event !== 'pull_request' || labels.includes('ci:full');
  const paths = files.flatMap(file => [file.filename, file.previous_filename].filter(Boolean));
  const every = full || files.length >= FILE_LIMIT || paths.some(path => EVERY_LANE.test(path));
  const hit = lane => paths.some(path => LANES[lane].some(rule => rule.test(path)));
  const macos = full || labels.includes('ci:macos') || hit('mac-native');
  const lanes = Object.keys(LANES).filter(lane => (lane === 'mac-native' ? macos : every || hit(lane)));
  return { lanes, full, macos, vitest: vitestTarget(lanes, every || paths.some(path => ALL_VITEST.test(path)), files) };
}

function vitestTarget(lanes, all, files) {
  if (!lanes.includes('frontend')) return '';
  const related = files
    .filter(file => file.status !== 'removed' && file.filename.startsWith('frontend/'))
    .map(file => file.filename.slice('frontend/'.length));
  if (all || related.length === 0 || related.some(path => /\s/.test(path))) return 'all';
  return related.join(' ');
}

export function outputs(pick) {
  return [
    `lanes=${JSON.stringify(pick.lanes)}`,
    `frontend=${pick.lanes.some(lane => FRONTEND_WORKFLOW_LANES.includes(lane))}`,
    `full=${pick.full}`,
    `vitest=${pick.vitest}`,
  ].join('\n') + '\n';
}

function gh(...args) {
  return execFileSync('gh', args, { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
}

function readPullRequest(repo, number) {
  const pr = JSON.parse(gh('api', `repos/${repo}/pulls/${number}`));
  const files = gh('api', '--paginate', `repos/${repo}/pulls/${number}/files`, '--jq', '.[] | [.status, .filename, .previous_filename // ""] | @tsv')
    .split('\n')
    .filter(Boolean)
    .map(line => {
      const [status, filename, previous] = line.split('\t');
      return { status, filename, previous_filename: previous || undefined };
    });
  const fork = pr.head.repo?.full_name !== pr.base.repo.full_name;
  return { draft: pr.draft, base: pr.base.ref, fork, labels: pr.labels.map(label => label.name), files };
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const event = process.env.GITHUB_EVENT_NAME;
  const input = event === 'pull_request' ? { event, ...readPullRequest(process.env.GITHUB_REPOSITORY, process.env.PR) } : { event };
  const pick = pickLanes(input);
  console.error(JSON.stringify({ ...input, files: input.files?.length, ...pick }));
  process.stdout.write(outputs(pick));
}
