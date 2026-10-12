// errorMessage is the text a failure is shown with: an Error's message, anything else as text.
export function errorMessage(e: unknown): string {
  if (e instanceof Error) {
    return e.message;
  }
  return String(e);
}
