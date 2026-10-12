// navigator.clipboard only exists in secure contexts (https or localhost), and the gateway is
// often opened over plain http on a LAN address. There the old execCommand route still works.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through to the legacy route */
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;top:0;left:0;opacity:0";
  document.body.appendChild(ta);
  ta.select();
  try {
    return legacyCopy(document);
  } catch {
    return false;
  } finally {
    ta.remove();
  }
}

// The one legacy command this file needs: copy the current selection. execCommand is deprecated,
// but on a page that is not a secure context it is the only way to reach the clipboard, so it is
// kept here, behind the Clipboard API, and nowhere else.
interface LegacyCopyDocument {
  execCommand(command: "copy"): boolean;
}

function legacyCopy(doc: LegacyCopyDocument): boolean {
  return doc.execCommand("copy");
}
