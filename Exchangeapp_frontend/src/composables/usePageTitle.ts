import { toValue, watch, type MaybeRefOrGetter } from 'vue';
import { setPageTitle } from '../utils/pageTitle';

export const usePageTitle = (
  source: MaybeRefOrGetter<string | null | undefined>,
  active: MaybeRefOrGetter<boolean> = true,
) => {
  watch(
    [
      () => toValue(source),
      () => toValue(active),
    ],
    ([value, isActive]) => {
      if (!isActive) {
        return;
      }
      setPageTitle(value);
    },
    { immediate: true },
  );
};
