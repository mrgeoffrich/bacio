// extractDispatchTags pulls the `<issue_id>` / `<mode>` /
// `<dispatch_id>` / `<command-name>` chips out of a bacio dispatch
// stub (BACI-125). Kept in its own pure-TS file so the test runner
// can import it without crossing into JSX.

export type DispatchTags = {
  issue?: string;
  mode?: string;
  dispatchId?: string;
  command?: string;
};

export function extractDispatchTags(text: string): DispatchTags {
  const out: DispatchTags = {};
  if (!text) return out;
  const issue = /<issue_id>([^<]+)<\/issue_id>/.exec(text);
  const mode = /<mode>([^<]+)<\/mode>/.exec(text);
  const dispatchId = /<dispatch_id>([^<]+)<\/dispatch_id>/.exec(text);
  const command = /<command-name>([^<]+)<\/command-name>/.exec(text);
  if (issue) out.issue = issue[1].trim();
  if (mode) out.mode = mode[1].trim();
  if (dispatchId) out.dispatchId = dispatchId[1].trim();
  if (command) out.command = command[1].trim();
  return out;
}
