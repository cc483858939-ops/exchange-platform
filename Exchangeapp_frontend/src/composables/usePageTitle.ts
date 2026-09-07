import { toValue, watch, type MaybeRefOrGetter } from 'vue';
import { setPageTitle } from '../utils/pageTitle';

export const usePageTitle = (
  source: MaybeRefOrGetter<string | null | undefined>,
) => {
  watch(
    () => toValue(source),
    value => setPageTitle(value),
    { immediate: true },
  );
};
