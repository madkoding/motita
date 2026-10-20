package agent

// workLog is what every round of a run printed, kept as the synthesis reads it: its first and
// last roundOutputChars bytes and its length. A long run of verbose commands used to hold every
// byte for the whole task, and the synthesis read only 16 KiB of it.
type workLog struct {
	head, tail []byte
	n          int
}

// Write adds p to the log; the tail is cut back to roundOutputChars when it doubles that.
func (l *workLog) Write(p []byte) (int, error) {
	l.n += len(p)
	l.head = append(l.head, p[:min(len(p), roundOutputChars-len(l.head))]...)
	l.tail = append(l.tail, p...)
	if len(l.tail) > 2*roundOutputChars {
		l.tail = append([]byte(nil), l.tail[len(l.tail)-roundOutputChars:]...)
	}
	return len(p), nil
}

// text is the log with extra after it, cut in its middle to roundOutputChars: the same text as
// truncateMiddle of the whole log and extra.
func (l *workLog) text(extra string) string {
	if l.n == len(l.tail) {
		return truncateMiddle(string(l.tail)+extra, roundOutputChars)
	}
	return cutMiddle(string(l.head), string(l.tail)+extra, l.n+len(extra), roundOutputChars)
}
