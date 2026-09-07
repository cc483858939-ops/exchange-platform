import { reactive, ref, watch } from 'vue';
import { defineStore } from 'pinia';
import { followUser, getUser, getUserFollowers, getUserFollowing, unfollowUser, } from '../services/userService';
import { useAuthStore } from './auth';
import { useProfileSessionStore } from './profileSession';
import { registerConnectionsSessionSync, syncExternalFollowState, } from './sessionSync';
const pageSize = 20;
const maxTargets = 8;
const normalizeID = (value) => {
    const numberValue = typeof value === 'string' && value.trim() ? Number(value) : value;
    return typeof numberValue === 'number'
        && Number.isSafeInteger(numberValue)
        && numberValue > 0
        ? numberValue
        : null;
};
const createModeSession = () => ({
    items: [],
    loaded: false,
    initialLoading: false,
    initialError: '',
    loadingMore: false,
    loadMoreError: '',
    hasMore: false,
    nextOffset: 0,
    stale: false,
    revalidating: false,
    revalidateError: '',
    scrollY: 0,
    pageRequestVersion: 0,
    freshnessVersion: 0,
    loadedUserIDs: new Set(),
});
const createTargetSession = (targetID, lastAccessedAt) => ({
    targetID,
    profile: null,
    profileLoaded: false,
    profileLoading: false,
    profileError: '',
    profileRequestVersion: 0,
    followers: createModeSession(),
    following: createModeSession(),
    lastAccessedAt,
});
export const useConnectionsSessionStore = defineStore('connectionsSession', () => {
    const authStore = useAuthStore();
    const profileSessionStore = useProfileSessionStore();
    const viewerID = ref(null);
    const viewerGeneration = ref(0);
    const sessions = reactive(new Map());
    const pendingMutationIDs = ref(new Set());
    const mutationErrors = ref(new Map());
    const mutationVersions = ref(new Map());
    const mutationSequence = ref(0);
    const activeTargetID = ref(null);
    const activeMode = ref(null);
    let accessClock = 0;
    const nextAccessTime = () => {
        accessClock = Math.max(accessClock + 1, Date.now());
        return accessClock;
    };
    const setPending = (userID, pending) => {
        const next = new Set(pendingMutationIDs.value);
        if (pending)
            next.add(userID);
        else
            next.delete(userID);
        pendingMutationIDs.value = next;
    };
    const setMutationError = (userID, message) => {
        const next = new Map(mutationErrors.value);
        if (message)
            next.set(userID, message);
        else
            next.delete(userID);
        mutationErrors.value = next;
    };
    const touch = (session) => {
        session.lastAccessedAt = nextAccessTime();
    };
    const evictLeastRecentlyUsed = () => {
        let leastRecentlyUsedID = null;
        let leastRecentlyUsedAt = Number.POSITIVE_INFINITY;
        sessions.forEach((candidate) => {
            if (candidate.lastAccessedAt < leastRecentlyUsedAt) {
                leastRecentlyUsedID = candidate.targetID;
                leastRecentlyUsedAt = candidate.lastAccessedAt;
            }
        });
        if (leastRecentlyUsedID !== null) {
            sessions.delete(leastRecentlyUsedID);
        }
    };
    const ensureTargetSession = (rawTargetID, shouldTouch = true) => {
        const targetID = normalizeID(rawTargetID);
        if (targetID === null)
            return null;
        let session = sessions.get(targetID);
        if (!session) {
            if (sessions.size >= maxTargets)
                evictLeastRecentlyUsed();
            sessions.set(targetID, createTargetSession(targetID, nextAccessTime()));
            session = sessions.get(targetID);
        }
        else if (shouldTouch) {
            touch(session);
        }
        return session;
    };
    const getTargetSession = (rawTargetID) => {
        const targetID = normalizeID(rawTargetID);
        return targetID === null ? undefined : sessions.get(targetID);
    };
    const getModeSession = (rawTargetID, mode) => getTargetSession(rawTargetID)?.[mode];
    const clearSessions = () => {
        sessions.clear();
        activeTargetID.value = null;
        activeMode.value = null;
        pendingMutationIDs.value = new Set();
        mutationErrors.value = new Map();
        mutationVersions.value.clear();
        mutationSequence.value += 1;
    };
    const setViewer = (rawViewerID) => {
        const nextViewerID = normalizeID(rawViewerID);
        if (nextViewerID === viewerID.value)
            return false;
        viewerID.value = nextViewerID;
        viewerGeneration.value += 1;
        clearSessions();
        return true;
    };
    const isCurrentTarget = (targetID, target, capturedViewerID, capturedViewerGeneration) => (authStore.isAuthenticated
        && viewerID.value === capturedViewerID
        && viewerGeneration.value === capturedViewerGeneration
        && sessions.get(targetID) === target);
    const isCurrentMode = (targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion) => (isCurrentTarget(targetID, target, capturedViewerID, capturedViewerGeneration)
        && target[mode] === modeSession
        && modeSession.pageRequestVersion === capturedRequestVersion);
    const seedProfile = (target) => {
        if (target.profileLoaded || target.profile)
            return target.profile;
        const cachedProfile = profileSessionStore.getSession(target.targetID)?.user;
        if (cachedProfile) {
            target.profile = cachedProfile;
            target.profileLoaded = true;
        }
        return target.profile;
    };
    const loadProfile = async (targetID, target, capturedViewerID, capturedViewerGeneration) => {
        seedProfile(target);
        if (target.profileLoaded || target.profileLoading)
            return;
        const requestVersion = ++target.profileRequestVersion;
        target.profileLoading = true;
        target.profileError = '';
        try {
            const profile = await getUser(targetID);
            if (!isCurrentTarget(targetID, target, capturedViewerID, capturedViewerGeneration)
                || target.profileRequestVersion !== requestVersion)
                return;
            target.profile = profile;
            target.profileLoaded = true;
        }
        catch {
            if (isCurrentTarget(targetID, target, capturedViewerID, capturedViewerGeneration)
                && target.profileRequestVersion === requestVersion) {
                target.profileError = 'Profile could not be loaded.';
            }
        }
        finally {
            if (isCurrentTarget(targetID, target, capturedViewerID, capturedViewerGeneration)
                && target.profileRequestVersion === requestVersion) {
                target.profileLoading = false;
            }
        }
    };
    const requestModePage = (targetID, mode, offset) => (mode === 'followers'
        ? getUserFollowers(targetID, { limit: pageSize, offset })
        : getUserFollowing(targetID, { limit: pageSize, offset }));
    const appendPage = (modeSession, page) => {
        const rawItems = page.items ?? [];
        modeSession.nextOffset += rawItems.length;
        const additions = rawItems.filter((item) => {
            if (modeSession.loadedUserIDs.has(item.user.id))
                return false;
            modeSession.loadedUserIDs.add(item.user.id);
            return true;
        });
        if (additions.length > 0) {
            modeSession.items = [...modeSession.items, ...additions];
        }
        modeSession.hasMore = Boolean(page.has_more);
        modeSession.loaded = true;
    };
    const resetModePage = (modeSession) => {
        modeSession.pageRequestVersion += 1;
        modeSession.freshnessVersion += 1;
        modeSession.items = [];
        modeSession.loaded = false;
        modeSession.initialLoading = false;
        modeSession.initialError = '';
        modeSession.loadingMore = false;
        modeSession.loadMoreError = '';
        modeSession.hasMore = false;
        modeSession.nextOffset = 0;
        modeSession.stale = false;
        modeSession.revalidating = false;
        modeSession.revalidateError = '';
        modeSession.loadedUserIDs.clear();
    };
    const loadInitial = async (rawTargetID, mode, force = false) => {
        const targetID = normalizeID(rawTargetID);
        const capturedViewerID = viewerID.value;
        if (targetID === null || capturedViewerID === null || !authStore.isAuthenticated)
            return;
        const target = getTargetSession(targetID);
        const modeSession = target?.[mode];
        if (!target || !modeSession)
            return;
        if (modeSession.initialLoading)
            return;
        if (modeSession.loaded && !force) {
            if (modeSession.stale)
                void revalidateMode(targetID, mode);
            return;
        }
        if (force)
            resetModePage(modeSession);
        const capturedViewerGeneration = viewerGeneration.value;
        const capturedFreshnessVersion = modeSession.freshnessVersion;
        const capturedRequestVersion = ++modeSession.pageRequestVersion;
        modeSession.initialLoading = true;
        modeSession.initialError = '';
        modeSession.loadMoreError = '';
        try {
            const page = await requestModePage(targetID, mode, 0);
            if (!isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion))
                return;
            appendPage(modeSession, page);
            if (capturedFreshnessVersion === modeSession.freshnessVersion) {
                modeSession.stale = false;
            }
            modeSession.revalidateError = '';
        }
        catch {
            if (isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)) {
                modeSession.initialError = 'Connections could not be loaded.';
            }
        }
        finally {
            if (isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)) {
                modeSession.initialLoading = false;
            }
        }
    };
    const activate = (rawTargetID, mode) => {
        const targetID = normalizeID(rawTargetID);
        const capturedViewerID = viewerID.value;
        if (targetID === null || capturedViewerID === null || !authStore.isAuthenticated)
            return null;
        const target = ensureTargetSession(targetID);
        if (!target)
            return null;
        activeTargetID.value = targetID;
        activeMode.value = mode;
        seedProfile(target);
        const capturedViewerGeneration = viewerGeneration.value;
        void loadProfile(targetID, target, capturedViewerID, capturedViewerGeneration);
        void loadInitial(targetID, mode);
        if (target[mode].loaded && target[mode].stale) {
            void revalidateMode(targetID, mode);
        }
        return target;
    };
    const reload = (rawTargetID, mode) => {
        const targetID = normalizeID(rawTargetID);
        if (targetID === null)
            return;
        const target = getTargetSession(targetID);
        if (!target || viewerID.value === null)
            return;
        resetModePage(target[mode]);
        void loadInitial(targetID, mode);
    };
    const loadMore = async (rawTargetID, mode) => {
        const targetID = normalizeID(rawTargetID);
        const capturedViewerID = viewerID.value;
        if (targetID === null || capturedViewerID === null || !authStore.isAuthenticated)
            return;
        const target = getTargetSession(targetID);
        const modeSession = target?.[mode];
        if (!target
            || !modeSession
            || !modeSession.loaded
            || !modeSession.hasMore
            || modeSession.loadingMore
            || modeSession.initialLoading
            || modeSession.stale
            || modeSession.revalidating)
            return;
        const capturedViewerGeneration = viewerGeneration.value;
        const offset = modeSession.nextOffset;
        const capturedRequestVersion = ++modeSession.pageRequestVersion;
        modeSession.loadingMore = true;
        modeSession.loadMoreError = '';
        try {
            const page = await requestModePage(targetID, mode, offset);
            if (!isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)
                || modeSession.nextOffset !== offset)
                return;
            appendPage(modeSession, page);
        }
        catch {
            if (isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)) {
                modeSession.loadMoreError = 'Could not load more users.';
            }
        }
        finally {
            if (isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)) {
                modeSession.loadingMore = false;
            }
        }
    };
    const revalidateMode = async (rawTargetID, mode) => {
        const targetID = normalizeID(rawTargetID);
        const capturedViewerID = viewerID.value;
        if (targetID === null || capturedViewerID === null || !authStore.isAuthenticated)
            return;
        const target = getTargetSession(targetID);
        const modeSession = target?.[mode];
        if (!target
            || !modeSession
            || !modeSession.loaded
            || !modeSession.stale
            || modeSession.revalidating)
            return;
        const capturedViewerGeneration = viewerGeneration.value;
        const capturedFreshnessVersion = modeSession.freshnessVersion;
        const capturedRequestVersion = ++modeSession.pageRequestVersion;
        modeSession.revalidating = true;
        modeSession.revalidateError = '';
        modeSession.loadingMore = false;
        try {
            const page = await requestModePage(targetID, mode, 0);
            if (!isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion))
                return;
            const oldByUserID = new Map(modeSession.items.map(item => [item.user.id, item]));
            const freshIDs = new Set();
            const freshItems = [];
            const rawItems = page.items ?? [];
            rawItems.forEach((item) => {
                if (freshIDs.has(item.user.id))
                    return;
                freshIDs.add(item.user.id);
                const oldItem = oldByUserID.get(item.user.id);
                freshItems.push(pendingMutationIDs.value.has(item.user.id) && oldItem
                    ? { ...item, following: oldItem.following }
                    : item);
            });
            const cachedTail = page.has_more
                ? modeSession.items.filter(item => !freshIDs.has(item.user.id))
                : [];
            modeSession.items = [...freshItems, ...cachedTail];
            modeSession.loadedUserIDs.clear();
            modeSession.items.forEach(item => modeSession.loadedUserIDs.add(item.user.id));
            modeSession.nextOffset = rawItems.length;
            modeSession.hasMore = Boolean(page.has_more);
            modeSession.loaded = true;
            modeSession.initialError = '';
            if (capturedFreshnessVersion === modeSession.freshnessVersion) {
                modeSession.stale = false;
                modeSession.revalidateError = '';
            }
            else {
                modeSession.stale = true;
            }
        }
        catch {
            if (isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)) {
                modeSession.stale = true;
                modeSession.revalidateError = 'Connections could not be refreshed.';
            }
        }
        finally {
            if (isCurrentMode(targetID, target, mode, modeSession, capturedViewerID, capturedViewerGeneration, capturedRequestVersion)) {
                modeSession.revalidating = false;
            }
        }
    };
    const applyRowsForUser = (userID, following) => {
        let applied = false;
        sessions.forEach((target) => {
            ['followers', 'following'].forEach((mode) => {
                target[mode].items.forEach((item) => {
                    if (item.user.id === userID && item.following !== following) {
                        item.following = following;
                        applied = true;
                    }
                });
            });
        });
        return applied;
    };
    const removeOwnFollowingRows = (userID) => {
        let removed = false;
        sessions.forEach((target) => {
            if (target.targetID !== viewerID.value)
                return;
            const modeSession = target.following;
            if (!modeSession.loadedUserIDs.has(userID))
                return;
            const previousLength = modeSession.items.length;
            modeSession.items = modeSession.items.filter(item => item.user.id !== userID);
            modeSession.loadedUserIDs.delete(userID);
            if (modeSession.items.length !== previousLength) {
                modeSession.nextOffset = Math.max(0, modeSession.nextOffset - 1);
                modeSession.pageRequestVersion += 1;
                modeSession.loadingMore = false;
                modeSession.revalidating = false;
                removed = true;
            }
        });
        return removed;
    };
    const markOwnFollowingStaleForUnknown = (userID) => {
        let marked = false;
        sessions.forEach((target) => {
            if (target.targetID !== viewerID.value)
                return;
            const modeSession = target.following;
            if (modeSession.loaded && !modeSession.loadedUserIDs.has(userID)) {
                modeSession.stale = true;
                modeSession.freshnessVersion += 1;
                modeSession.pageRequestVersion += 1;
                modeSession.loadingMore = false;
                modeSession.revalidating = false;
                marked = true;
            }
        });
        return marked;
    };
    const applyExternalFollowStateLocal = (state) => {
        const userID = normalizeID(state.user_id);
        if (userID === null)
            return false;
        mutationSequence.value += 1;
        mutationVersions.value.set(userID, mutationSequence.value);
        setPending(userID, false);
        setMutationError(userID);
        const applied = applyRowsForUser(userID, Boolean(state.following));
        const removed = state.following
            ? false
            : removeOwnFollowingRows(userID);
        const markedStale = state.following
            ? markOwnFollowingStaleForUnknown(userID)
            : false;
        return applied || removed || markedStale;
    };
    const toggleFollow = async (rawTargetOrUserID, requestedMode, rawUserID) => {
        const usesActiveRoute = rawUserID === undefined;
        const targetID = usesActiveRoute
            ? activeTargetID.value
            : normalizeID(rawTargetOrUserID);
        const mode = usesActiveRoute ? activeMode.value : requestedMode ?? null;
        const normalizedUserID = normalizeID(usesActiveRoute ? rawTargetOrUserID : rawUserID);
        const capturedViewerID = viewerID.value;
        if (targetID === null
            || mode === null
            || normalizedUserID === null
            || capturedViewerID === null
            || normalizedUserID === capturedViewerID
            || pendingMutationIDs.value.has(normalizedUserID))
            return;
        const target = getTargetSession(targetID);
        const modeSession = target?.[mode];
        const item = modeSession?.items.find(candidate => candidate.user.id === normalizedUserID);
        if (!target || !modeSession || !item)
            return;
        const previous = item.following;
        const previousRows = [];
        sessions.forEach((candidateTarget) => {
            ['followers', 'following'].forEach((candidateMode) => {
                const candidateModeSession = candidateTarget[candidateMode];
                if (candidateModeSession.items.some(candidate => candidate.user.id === normalizedUserID)) {
                    previousRows.push({
                        modeSession: candidateModeSession,
                        following: candidateModeSession.items.find(candidate => candidate.user.id === normalizedUserID).following,
                    });
                }
            });
        });
        const capturedViewerGeneration = viewerGeneration.value;
        const mutationVersion = mutationSequence.value + 1;
        mutationSequence.value = mutationVersion;
        mutationVersions.value.set(normalizedUserID, mutationVersion);
        setPending(normalizedUserID, true);
        setMutationError(normalizedUserID);
        applyRowsForUser(normalizedUserID, !previous);
        const mutationCurrent = () => (authStore.isAuthenticated
            && viewerID.value === capturedViewerID
            && viewerGeneration.value === capturedViewerGeneration
            && mutationVersions.value.get(normalizedUserID) === mutationVersion
            && pendingMutationIDs.value.has(normalizedUserID));
        try {
            const response = previous
                ? await unfollowUser(normalizedUserID)
                : await followUser(normalizedUserID);
            if (!mutationCurrent())
                return;
            if (response.user_id !== normalizedUserID) {
                throw new Error('Invalid follow response');
            }
            applyRowsForUser(normalizedUserID, response.following);
            setPending(normalizedUserID, false);
            setMutationError(normalizedUserID);
            syncExternalFollowState(response);
        }
        catch {
            if (!mutationCurrent())
                return;
            previousRows.forEach(({ modeSession: candidateModeSession, following }) => {
                candidateModeSession.items.forEach((candidate) => {
                    if (candidate.user.id === normalizedUserID)
                        candidate.following = following;
                });
            });
            setPending(normalizedUserID, false);
            setMutationError(normalizedUserID, 'Could not update follow status.');
        }
    };
    const replaceUserIdentityLocal = (author) => {
        let applied = false;
        sessions.forEach((target) => {
            if (target.profile?.id === author.id) {
                target.profile = { ...target.profile, ...author };
                applied = true;
            }
            ['followers', 'following'].forEach((mode) => {
                target[mode].items.forEach((item) => {
                    if (item.user.id !== author.id)
                        return;
                    item.user = { ...item.user, ...author };
                    applied = true;
                });
            });
        });
        return applied;
    };
    const saveScroll = (rawTargetID, mode, value) => {
        const targetID = normalizeID(rawTargetID);
        const target = targetID === null ? undefined : sessions.get(targetID);
        const modeSession = target?.[mode];
        if (modeSession && Number.isFinite(value) && value >= 0) {
            modeSession.scrollY = value;
            touch(target);
        }
    };
    registerConnectionsSessionSync({
        applyExternalFollowStateLocal,
        replaceUserIdentityLocal,
    });
    watch(() => authStore.isAuthenticated ? authStore.currentIdentity?.id : null, nextViewerID => {
        setViewer(nextViewerID ?? null);
    }, { immediate: true });
    return {
        viewerID,
        viewerGeneration,
        sessions,
        pendingMutationIDs,
        mutationErrors,
        mutationVersions,
        mutationSequence,
        maxTargets,
        setViewer,
        getTargetSession,
        getModeSession,
        activate,
        loadInitial,
        reload,
        loadMore,
        revalidateMode,
        toggleFollow,
        applyExternalFollowStateLocal,
        replaceUserIdentityLocal,
        saveScroll,
    };
});
