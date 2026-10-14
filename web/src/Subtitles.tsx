// Subtitles show on screen, like a film's, the sentence Motita is saying aloud: whichever voice
// speaks (the device, the gateway, a local one), it follows the sentence the reader starts
// (SUBTITLE_EVENT), and fades out when the voice ends or is stopped. It sits at the bottom of the
// conversation, centred above the composer, and never takes a click.
import { useEffect, useState } from "preact/hooks";
import { SUBTITLE_EVENT, type SubtitleSize } from "./speech";

export function Subtitles({ on, size }: { on: boolean; size: SubtitleSize }) {
  // text stays while the subtitle fades out, so it does not empty before it disappears.
  const [text, setText] = useState("");
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!on) {
      setShown(false);
      return undefined;
    }
    const onSubtitle = (e: Event) => {
      const said = (e as CustomEvent<string>).detail;
      if (said) {
        setText(said);
      }
      setShown(!!said);
    };
    window.addEventListener(SUBTITLE_EVENT, onSubtitle);
    return () => window.removeEventListener(SUBTITLE_EVENT, onSubtitle);
  }, [on]);
  if (!on) {
    return null;
  }
  // A polite live region: a screen reader says the line once, without cutting what it was saying.
  return (
    <div class="subtitles-anchor" data-testid="subtitles">
      <p
        class={"subtitles subtitles-" + size}
        data-shown={shown ? "1" : undefined}
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        {text}
      </p>
    </div>
  );
}
