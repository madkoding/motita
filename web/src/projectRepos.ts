// The folder a repository address names.

// repoFolderOf is the folder the gateway gives a repository: the URL's last element, without a
// trailing slash or ".git". The same rule as the gateway's, so the list shows the name it will use.
export function repoFolderOf(url: string): string {
  let u = url.trim();
  while (u.endsWith("/")) {
    u = u.slice(0, -1);
  }
  if (u.endsWith(".git")) {
    u = u.slice(0, -".git".length);
  }
  return u.slice(
    Math.max(u.lastIndexOf("/"), u.lastIndexOf(":"), u.lastIndexOf("\\")) + 1,
  );
}
