// Repository URLs are validated when stored. Recheck here because a URL can
// also come from an older database or a separately deployed server.
export function commitLink(repositoryURL: string, hash: string): string | undefined {
  if (!repositoryURL || !/^[0-9a-fA-F]{7,128}$/.test(hash)) return undefined;
  try {
    const url = new URL(repositoryURL);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password ||
        url.search || url.hash || url.pathname === '/') return undefined;
    const host = url.hostname.toLowerCase();
    if (host !== 'github.com' && host !== 'gitlab.com' && host !== 'bitbucket.org')
      return undefined;
    const root = url.href.replace(/\/+$/, '').replace(/\.git$/, '');
    const path = host === 'gitlab.com' ? '/-/commit/' : host === 'bitbucket.org' ? '/commits/' : '/commit/';
    return root + path + hash;
  } catch {
    return undefined;
  }
}
