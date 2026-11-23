/** Public origin of the website; canonical URLs and the sitemap derive from it. */
export const SITE_URL = 'https://google-contacts-mcp.samber.dev';

/** Product name, shown in titles, structured data and llms.txt. */
export const SITE_NAME = 'Google Contacts MCP';

/** One-sentence description of the service, reused by structured data and llms.txt. */
export const SITE_SUMMARY =
  'A remote MCP server that lets Claude and ChatGPT search, read, create and enrich your Google Contacts, with your consent.';

/** Connector URL users paste into Claude or ChatGPT: the MCP server's endpoint. */
export const MCP_URL = 'https://mcp.google-contacts-mcp.samber.dev/mcp';

/** Source repository of the open-source server. */
export const REPO_URL = 'https://github.com/samber/google-contacts-mcp';

/** Person who operates the hosted instance, named in the policy and the terms. */
export const OPERATOR_NAME = 'Samuel Berthe';

/** The operator's LinkedIn profile, linked from the operator's name in the footer. */
export const OPERATOR_LINKEDIN = 'https://www.linkedin.com/in/samuelberthe';

/** The operator's GitHub profile, the owner of the repository above. */
export const OPERATOR_GITHUB = 'https://github.com/samber';

/** Documentation in the repository, linked from llms.txt and structured data. */
export const SELF_HOSTING_URL = `${REPO_URL}/blob/main/docs/self-hosting.md`;
export const SECURITY_URL = `${REPO_URL}/blob/main/docs/security.md`;

/** Days of inactivity after which the server purges an account. Matches `inactivityWindow` in internal/jobs. */
export const INACTIVITY_DAYS = 180;

/** Public contact address for privacy and terms questions. */
export const OPERATOR_EMAIL = 'hello@samuel-berthe.fr';

export type ToolGroup = 'find' | 'write' | 'label';

export type Tool = {
  /** MCP tool name, exactly as registered in internal/tools; scripts/check-dist.mjs compares them. */
  name: string;
  /** What it does, in the user's words. */
  summary: string;
  group: ToolGroup;
};

export const TOOLS: readonly Tool[] = [
  { name: 'search_contacts', summary: 'Find people by the start of a name, e-mail, phone or company.', group: 'find' },
  { name: 'list_contacts', summary: 'Browse your whole address book, page by page.', group: 'find' },
  { name: 'get_contact', summary: 'Read everything stored on one contact.', group: 'find' },
  { name: 'create_contact', summary: 'Add a new contact.', group: 'write' },
  { name: 'update_contact', summary: 'Change only the fields you give. A list you give replaces the old list.', group: 'write' },
  {
    name: 'upsert_contact_by_email',
    summary: 'Find a contact by e-mail and fill its empty fields, or create it. Never overwrites a value.',
    group: 'write',
  },
  { name: 'delete_contact', summary: 'Delete a contact for good.', group: 'write' },
  { name: 'list_contact_groups', summary: 'See your labels.', group: 'label' },
  { name: 'create_contact_group', summary: 'Create a label.', group: 'label' },
  { name: 'modify_contact_group_members', summary: 'Add contacts to a label, or remove them.', group: 'label' },
];

export const TOOL_GROUPS: readonly { group: ToolGroup; title: string }[] = [
  { group: 'find', title: 'Find your people' },
  { group: 'write', title: 'Keep them current' },
  { group: 'label', title: 'Organize with labels' },
];

/**
 * What the server keeps, in plain words for non-technical visitors. Same facts as README
 * "What is stored" and the privacy policy, which give the technical detail.
 */
export const STORED: readonly string[] = [
  'Your Google account id and e-mail address',
  'A fingerprint of the pass your assistant uses, never the pass itself',
  'Which assistant apps connected, such as Claude or ChatGPT',
];

/** What the server never keeps. Mirrors README "What is stored" and the privacy policy. */
export const NEVER_STORED: readonly string[] = [
  'Your contacts: names, e-mail addresses, phone numbers, notes or any other field',
  'Tool inputs and outputs exchanged with your assistant',
  'Any token in clear',
  'Contact data, tool arguments or results in logs at the default log level',
];

export type FaqEntry = {
  question: string;
  /** Plain text: the same string feeds the page, the FAQPage structured data and llms.txt. */
  answer: string;
};

/** Questions people ask before connecting. Every answer restates a fact from the README, the privacy policy or the code. */
export const FAQ: readonly FaqEntry[] = [
  {
    question: 'Can Claude access my Google Contacts?',
    answer:
      'Yes. Add the connector URL to Claude as a custom connector, sign in with Google and approve access. Claude can then search, read, create, update and label your contacts. You can revoke access at any time from your Google account.',
  },
  {
    question: 'Does it work with ChatGPT?',
    answer:
      'Yes. Turn on developer mode in ChatGPT, create an app with the same connector URL and sign in with Google. It works the same way as in Claude.',
  },
  {
    question: 'Is it free?',
    answer:
      'Yes. The hosted service is free, with no promise of availability, and the server is open source under the MIT license. You can also run your own copy.',
  },
  {
    question: 'Does it store my contacts?',
    answer:
      'No. Every tool call reads or writes Google directly. The server keeps only what it needs to keep you signed in: your Google account id and e-mail address, your Google tokens (encrypted), and a fingerprint of the pass your assistant uses. Contacts are never stored, and tool arguments or results are not logged at the default log level.',
  },
  {
    question: 'Can it delete contacts?',
    answer:
      'Yes. The delete_contact tool deletes a contact permanently, and the server flags it as destructive. Tools that only read are flagged as read-only. The upsert_contact_by_email tool never overwrites a value.',
  },
  {
    question: 'How do I disconnect and erase my data?',
    answer: `Revoke access at myaccount.google.com/permissions: the server deletes your data the next time your assistant uses the connector. You can also e-mail the operator at ${OPERATOR_EMAIL} to be disconnected. An account unused for ${INACTIVITY_DAYS} days is purged automatically.`,
  },
];

/** Date (YYYY-MM-DD) of the last change to the privacy policy or the terms. Update it with every edit of either page. */
export const LEGAL_UPDATED = '2026-11-20';
