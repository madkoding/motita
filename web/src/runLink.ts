// The link between this tab and the stream of ONE run.
//
// A run belongs to its SESSION, and the tab only ever draws one session at a time. The stream of a
// run used to be read by whatever conversation happened to be on screen: open a new session while
// the agent works in another one, and the old run's steps, commands and answer were painted into
// the new conversation, while its `running` flag kept the new one's composer locked. Every stream
// now carries the session it was opened for and an AbortController, and an event is drawn only
// while that link is still the tab's current one AND its session is the one on screen. Leaving the
// session aborts the link; the run keeps going in the gateway, and coming back follows it again.
//
// Kept free of imports so the node test can load it directly.

export interface RunLink {
  sid: string
  ctrl: AbortController
}

export interface LinkHolder {
  current: RunLink | null
}

// openLink makes a new link the tab's current one, cutting the one it replaces.
export function openLink(holder: LinkHolder, sid: string): RunLink {
  holder.current?.ctrl.abort()
  const link = { sid, ctrl: new AbortController() }
  holder.current = link
  return link
}

// isLive says whether a link may still draw: it is the tab's current one, it was not cut, and its
// session is the one on screen.
export function isLive(link: RunLink, holder: LinkHolder, onScreen: string): boolean {
  return holder.current === link && !link.ctrl.signal.aborted && link.sid === onScreen
}

// cutLink aborts the tab's current link and forgets it. It returns the session that link was for,
// or '' when there was none.
export function cutLink(holder: LinkHolder): string {
  const link = holder.current
  holder.current = null
  if (!link) { return '' }
  link.ctrl.abort()
  return link.sid
}
