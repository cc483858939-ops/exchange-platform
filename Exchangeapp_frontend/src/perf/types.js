export const getPerfGitHead = () => (typeof __EXCHANGE_PERF_GIT_HEAD__ === 'string' && __EXCHANGE_PERF_GIT_HEAD__.trim()
    ? __EXCHANGE_PERF_GIT_HEAD__.trim()
    : 'unknown');
