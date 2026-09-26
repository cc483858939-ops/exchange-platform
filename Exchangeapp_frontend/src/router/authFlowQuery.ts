import type { LocationQuery, LocationQueryRaw } from 'vue-router';

export const buildAuthFlowQuery = (query: LocationQuery): LocationQueryRaw => {
  const authFlowQuery: LocationQueryRaw = {};

  if (typeof query.returnTo === 'string') {
    authFlowQuery.returnTo = query.returnTo;
  }

  if (query.intent === 'profile') {
    authFlowQuery.intent = 'profile';
  }

  return authFlowQuery;
};
