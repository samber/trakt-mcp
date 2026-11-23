import type { APIRoute } from 'astro';
import {
  FAQ,
  MCP_URL,
  NOT_AFFILIATED,
  OPERATOR_NAME,
  NEVER_STORED,
  REPO_URL,
  SECURITY_URL,
  SELF_HOSTING_URL,
  SITE_NAME,
  SITE_SUMMARY,
  SITE_URL,
  STORED,
  TOOLS,
  TOOL_GROUPS,
  TRAKT_CONNECTED_APPS_URL,
} from '../data/site';

// Built from src/data/site.ts, the list the gate compares with internal/tools, so the
// summary for assistants cannot drift from the tools and the privacy facts on the pages.
const toolSection = TOOL_GROUPS.flatMap(({ group, title }) => [
  `### ${title}`,
  '',
  ...TOOLS.filter((tool) => tool.group === group).map((tool) => `- \`${tool.name}\`: ${tool.summary}`),
  '',
]);

const lines = [
  `# ${SITE_NAME}`,
  '',
  `> ${SITE_SUMMARY} Every call goes to Trakt directly; viewing data is never stored. Open source, written in Go. ${NOT_AFFILIATED}`,
  '',
  `${SITE_NAME} is a hosted remote MCP server for Trakt: paste one URL into Claude or ChatGPT, with no stdio, npx or local install. It is free, MIT licensed, and operated by ${OPERATOR_NAME}.`,
  '',
  '## How to connect',
  '',
  `Connector URL to add in an assistant: ${MCP_URL}`,
  '',
  '1. Claude: Settings, Connectors, Add custom connector. ChatGPT: turn on developer mode, then Create app.',
  '2. Paste the connector URL.',
  `3. Sign in with Trakt and approve access to your Trakt account. Access can be revoked at any time in Trakt, under Settings, Connected Apps (${TRAKT_CONNECTED_APPS_URL}).`,
  '',
  '## Tools',
  '',
  ...toolSection,
  '## What the server stores',
  '',
  ...STORED.map((item) => `- ${item}`),
  '',
  '## What the server never stores',
  '',
  ...NEVER_STORED.map((item) => `- ${item}`),
  '',
  '## FAQ',
  '',
  ...FAQ.flatMap(({ question, answer }) => [`### ${question}`, '', answer, '']),
  '## Pages',
  '',
  `- [Home](${SITE_URL}/): what the service does and how to connect it.`,
  `- [Privacy policy](${SITE_URL}/privacy): what is stored, what is never stored, and how to erase it.`,
  `- [Terms of service](${SITE_URL}/terms): the conditions for using the hosted service.`,
  '',
  '## Source',
  '',
  `- [Source code and documentation](${REPO_URL}): the Go server and the list of tools.`,
  `- [Self-hosting guide](${SELF_HOSTING_URL}): run your own copy.`,
  `- [Security notes](${SECURITY_URL}): how OAuth, tokens and Trakt credentials are handled.`,
  '',
];

export const GET: APIRoute = () =>
  new Response(lines.join('\n'), { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
