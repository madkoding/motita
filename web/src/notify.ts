// showNotification puts up a system notification, the way each platform accepts it.
//
// Android's Chrome refuses `new Notification()` outright (it throws "Illegal constructor"): there a
// page may only notify through its service worker. So the worker is asked first, and the
// constructor is what a desktop browser without a registered worker falls back to. It resolves to
// whether a notification went up.
export async function showNotification(
  title: string,
  options: NotificationOptions,
): Promise<boolean> {
  try {
    const reg = await navigator.serviceWorker?.getRegistration();
    if (reg) {
      await reg.showNotification(title, options);
      return true;
    }
  } catch {
    /* no worker, or it refused: try the constructor */
  }
  try {
    new Notification(title, options);
    return true;
  } catch {
    return false;
  }
}
