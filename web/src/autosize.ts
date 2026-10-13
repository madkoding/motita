// fitToContent grows a one-row textarea to its text, up to its CSS max-height. Without it the
// composer's draft stayed one row tall once it wrapped and scrolled inside that row, so on a phone
// the reader saw the bottom half of one line and the top half of the next.
export function fitToContent(el: HTMLTextAreaElement): void {
  el.style.height = "auto";
  el.style.height = `${el.scrollHeight}px`;
}
