export type CopyResult = 'copied' | 'unavailable';

type ClipboardLike = Pick<Clipboard, 'writeText'>;

/**
 * Writes text to the clipboard. Never throws: a missing API (insecure context,
 * old browser) and a refused write both report "unavailable", so the caller
 * can fall back to selecting the text for the user.
 * `onCopied` runs only after a successful write; an error inside it is swallowed.
 */
export async function copyText(text: string, clipboard: ClipboardLike | undefined, onCopied?: () => void): Promise<CopyResult> {
  if (!clipboard) return 'unavailable';
  try {
    await clipboard.writeText(text);
  } catch {
    return 'unavailable';
  }
  try {
    onCopied?.();
  } catch {
    // A failing hook must not turn a successful copy into a failure.
  }
  return 'copied';
}
