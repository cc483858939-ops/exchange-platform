import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import AuthorIdentity from '../AuthorIdentity.vue';
import LinkifiedText from '../content/LinkifiedText.vue';
import PostMediaGrid from '../content/PostMediaGrid.vue';
import ConfirmDialog from '../dialogs/ConfirmDialog.vue';
import LikeAction from '../engagement/LikeAction.vue';
import RepostAction from '../engagement/RepostAction.vue';
import AppIcon from '../icons/AppIcon.vue';
import { getPostViewTelemetry } from '../../services/postViewTelemetry';
import { usePostDetailHandoffStore } from '../../store/postDetailHandoff';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../../utils/engagementCount';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
let __VLS_typeProps;
const props = withDefaults(defineProps(), {
    trackView: true,
    likePending: false,
    repostPending: false,
    showNotInterested: false,
    showDelete: false,
    deletePending: false,
    deleteError: '',
});
const emit = defineEmits();
const router = useRouter();
const postViewTelemetry = getPostViewTelemetry();
const postDetailHandoff = usePostDetailHandoffStore();
const postCardRef = ref(null);
const bodyRef = ref(null);
const moreButtonRef = ref(null);
const menuRef = ref(null);
const menuItemRefs = ref([]);
const moreOpen = ref(false);
const deleteConfirmOpen = ref(false);
const bodyExpanded = ref(false);
const bodyOverflowing = ref(false);
let bodyResizeObserver = null;
const copyState = ref('idle');
let copyRequestVersion = 0;
const likeLoading = computed(() => props.post.likeStatus === 'unknown');
const likeUnavailable = computed(() => props.post.likeStatus === 'unavailable');
const repostLoading = computed(() => props.post.repostStatus === 'unknown');
const repostUnavailable = computed(() => props.post.repostStatus === 'unavailable');
const referencePost = computed(() => props.post.quotePost ?? props.post.replyToPost ?? null);
const referenceDestination = computed(() => {
    const reference = referencePost.value;
    if (!reference || reference.deleted) {
        return undefined;
    }
    return {
        name: 'PostDetail',
        params: { id: String(reference.id) },
    };
});
const referenceAuthor = computed(() => (referencePost.value && !referencePost.value.deleted
    ? referencePost.value.author
    : null));
const referenceContent = computed(() => {
    const reference = referencePost.value;
    if (!reference || reference.deleted) {
        return '';
    }
    return reference.content || 'Post';
});
const referenceMedia = computed(() => {
    const reference = referencePost.value;
    return reference && !reference.deleted ? reference.media : [];
});
const handleLikeActivation = () => {
    if (props.post.likeStatus !== 'ready' || props.likePending) {
        return;
    }
    emit('toggleLike', props.post.id);
};
const handleRepostActivation = () => {
    if (props.post.repostStatus !== 'ready' || props.repostPending) {
        return;
    }
    emit('toggleRepost', props.post.id);
};
const actorLabel = (author) => (author.display_name?.trim()
    || (author.username?.trim() ? '@' + author.username.trim() : '')
    || 'User');
const repostContextLabel = computed(() => (`${actorLabel(props.post.repostContext?.actor ?? props.post.author)} reposted`));
const isNormalSameTabNavigation = (event) => (event.button === 0
    && !event.ctrlKey
    && !event.metaKey
    && !event.shiftKey
    && !event.altKey
    && !event.defaultPrevented);
const prepareDetailNavigation = (event) => {
    if (isNormalSameTabNavigation(event)) {
        postDetailHandoff.remember(props.post);
    }
    emit('postClick', props.post);
};
const likeLabel = computed(() => {
    const countLabel = String(props.post.likeCount)
        + (props.post.likeCount === 1 ? ' like' : ' likes');
    if (props.post.likeStatus === 'unavailable') {
        return 'Like unavailable, ' + countLabel;
    }
    if (props.post.likeStatus !== 'ready' || !props.post.liked) {
        return 'Like post, ' + countLabel;
    }
    return 'Unlike post, ' + countLabel;
});
const replyLabel = computed(() => {
    const countLabel = String(props.post.replyCount)
        + (props.post.replyCount === 1 ? ' reply' : ' replies');
    return 'Reply to post, ' + countLabel;
});
const compactViewCount = computed(() => formatCompactEngagementCount(props.post.viewCount));
const viewLabel = computed(() => formatAccessibleEngagementCount(props.post.viewCount, 'views'));
const viewActionLabel = computed(() => 'Open post, ' + viewLabel.value);
const observeCurrentPost = () => {
    if (props.trackView && postCardRef.value) {
        postViewTelemetry.observeFeedCard(postCardRef.value, props.post.id);
    }
};
const unobserveCurrentPost = () => {
    if (postCardRef.value) {
        postViewTelemetry.unobserveFeedCard(postCardRef.value);
    }
};
const copyActionLabel = computed(() => {
    if (copyState.value === 'success') {
        return 'Copied';
    }
    if (copyState.value === 'error') {
        return 'Copy failed';
    }
    return 'Copy link';
});
const syncMenuItemRefs = () => {
    menuItemRefs.value = menuRef.value
        ? Array.from(menuRef.value.querySelectorAll('[role="menuitem"]'))
        : [];
};
const getMenuItems = () => {
    syncMenuItemRefs();
    return menuItemRefs.value;
};
const setMenuItemRef = () => {
    void nextTick(syncMenuItemRefs);
};
const focusMenuItem = (index) => {
    const items = getMenuItems();
    if (items.length === 0) {
        return;
    }
    items[(index + items.length) % items.length]?.focus();
};
const removeOutsideListener = () => {
    document.removeEventListener('pointerdown', handleDocumentPointerDown);
};
const closeMore = (restoreFocus = false) => {
    const wasOpen = moreOpen.value;
    copyRequestVersion += 1;
    moreOpen.value = false;
    copyState.value = 'idle';
    menuItemRefs.value = [];
    removeOutsideListener();
    if (restoreFocus && wasOpen) {
        void nextTick(() => moreButtonRef.value?.focus());
    }
};
const handleDocumentPointerDown = (event) => {
    const target = event.target;
    if (!(target instanceof Node)) {
        return;
    }
    if (moreButtonRef.value?.contains(target) || menuRef.value?.contains(target)) {
        return;
    }
    closeMore();
};
const openMore = async () => {
    if (moreOpen.value) {
        return;
    }
    moreOpen.value = true;
    document.addEventListener('pointerdown', handleDocumentPointerDown);
    await nextTick();
    if (moreOpen.value) {
        focusMenuItem(0);
    }
};
const toggleMore = () => {
    if (moreOpen.value) {
        closeMore(true);
        return;
    }
    void openMore();
};
const handleMenuKeydown = (event) => {
    const items = getMenuItems();
    if (items.length === 0) {
        return;
    }
    const currentIndex = items.indexOf(document.activeElement);
    if (event.key === 'ArrowDown') {
        event.preventDefault();
        focusMenuItem(currentIndex < 0 ? 0 : currentIndex + 1);
    }
    else if (event.key === 'ArrowUp') {
        event.preventDefault();
        focusMenuItem(currentIndex < 0 ? items.length - 1 : currentIndex - 1);
    }
    else if (event.key === 'Home') {
        event.preventDefault();
        focusMenuItem(0);
    }
    else if (event.key === 'End') {
        event.preventDefault();
        focusMenuItem(items.length - 1);
    }
    else if (event.key === 'Escape') {
        event.preventDefault();
        closeMore(true);
    }
    else if (event.key === 'Tab') {
        closeMore();
    }
};
const isCurrentCopyRequest = (requestVersion, postId) => requestVersion === copyRequestVersion
    && postId === props.post.id
    && moreOpen.value;
const copyLink = async () => {
    const requestVersion = ++copyRequestVersion;
    const postId = props.post.id;
    try {
        const resolved = router.resolve({
            name: 'PostDetail',
            params: { id: String(postId) },
        });
        const url = new URL(resolved.href, window.location.origin).toString();
        if (!navigator.clipboard) {
            throw new Error('Clipboard API unavailable');
        }
        await navigator.clipboard.writeText(url);
        if (!isCurrentCopyRequest(requestVersion, postId)) {
            return;
        }
        copyState.value = 'success';
    }
    catch {
        if (!isCurrentCopyRequest(requestVersion, postId)) {
            return;
        }
        copyState.value = 'error';
    }
};
const handleNotInterested = () => {
    closeMore();
    emit('notInterested', props.post.id);
};
const requestDeletePost = () => {
    if (props.deletePending) {
        return;
    }
    closeMore();
    deleteConfirmOpen.value = true;
};
const confirmDeletePost = () => {
    if (props.deletePending) {
        return;
    }
    emit('deletePost', props.post.id);
};
const cancelDeletePost = () => {
    if (props.deletePending) {
        return;
    }
    deleteConfirmOpen.value = false;
    void nextTick(() => moreButtonRef.value?.focus());
};
const measureBodyOverflow = () => {
    const body = bodyRef.value;
    if (!body || bodyExpanded.value) {
        return;
    }
    const overflowing = body.scrollHeight > body.clientHeight + 1;
    if (bodyOverflowing.value !== overflowing) {
        bodyOverflowing.value = overflowing;
    }
};
const scheduleBodyOverflowMeasurement = () => {
    void nextTick(measureBodyOverflow);
};
const expandBody = () => {
    bodyExpanded.value = true;
};
watch([() => props.post.id, () => props.post.content], () => {
    bodyExpanded.value = false;
    bodyOverflowing.value = false;
    scheduleBodyOverflowMeasurement();
});
watch([() => props.post.id, () => props.trackView], ([postID, trackView], [previousPostID, previousTrackView]) => {
    if (postID === previousPostID && trackView === previousTrackView) {
        return;
    }
    unobserveCurrentPost();
    if (trackView) {
        observeCurrentPost();
    }
    if (postID !== previousPostID) {
        closeMore();
        deleteConfirmOpen.value = false;
    }
});
onMounted(() => {
    observeCurrentPost();
    scheduleBodyOverflowMeasurement();
    if (typeof ResizeObserver !== 'undefined' && bodyRef.value) {
        bodyResizeObserver = new ResizeObserver(() => {
            if (!bodyExpanded.value) {
                scheduleBodyOverflowMeasurement();
            }
        });
        bodyResizeObserver.observe(bodyRef.value);
    }
});
onBeforeUnmount(() => {
    unobserveCurrentPost();
    bodyResizeObserver?.disconnect();
    bodyResizeObserver = null;
    closeMore();
    deleteConfirmOpen.value = false;
});
const repostLabel = computed(() => {
    const countLabel = String(props.post.repostCount)
        + (props.post.repostCount === 1 ? ' repost' : ' reposts');
    if (props.post.repostStatus === 'unavailable') {
        return 'Repost unavailable, ' + countLabel;
    }
    if (props.post.repostStatus !== 'ready' || !props.post.reposted) {
        return 'Repost post, ' + countLabel;
    }
    return 'Undo repost, ' + countLabel;
});
const __VLS_withDefaultsArg = (function (t) { return t; })({
    trackView: true,
    likePending: false,
    repostPending: false,
    showNotInterested: false,
    showDelete: false,
    deletePending: false,
    deleteError: '',
});
const __VLS_fnComponent = (await import('vue')).defineComponent({
    emits: {},
});
let __VLS_functionalComponentProps;
let __VLS_modelEmitsType;
function __VLS_template() {
    let __VLS_ctx;
    /* Components */
    let __VLS_otherComponents;
    let __VLS_own;
    let __VLS_localComponents;
    let __VLS_components;
    let __VLS_styleScopedClasses;
    // CSS variable injection 
    // CSS variable injection end 
    let __VLS_resolvedLocalAndGlobalComponents;
    __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ ref: ("postCardRef"), ...{ class: ("post-card") }, });
    // @ts-ignore
    (__VLS_ctx.postCardRef);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__header") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__author-stack") }, });
    if (__VLS_ctx.post.repostContext) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__repost-context") }, });
        // @ts-ignore
        [AppIcon,];
        const __VLS_0 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("repost"), size: ((14)), }));
        const __VLS_1 = __VLS_0({ name: ("repost"), size: ((14)), }, ...__VLS_functionalComponentArgsRest(__VLS_0));
        ({}({ name: ("repost"), size: ((14)), }));
        // @ts-ignore
        [postCardRef, post,];
        const __VLS_4 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_1);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.repostContextLabel);
        // @ts-ignore
        [repostContextLabel,];
    }
    // @ts-ignore
    [AuthorIdentity,];
    const __VLS_5 = __VLS_asFunctionalComponent(AuthorIdentity, new AuthorIdentity({ author: ((__VLS_ctx.post.author)), createdAt: ((__VLS_ctx.post.createdAt)), }));
    const __VLS_6 = __VLS_5({ author: ((__VLS_ctx.post.author)), createdAt: ((__VLS_ctx.post.createdAt)), }, ...__VLS_functionalComponentArgsRest(__VLS_5));
    ({}({ author: ((__VLS_ctx.post.author)), createdAt: ((__VLS_ctx.post.createdAt)), }));
    // @ts-ignore
    [post, post,];
    const __VLS_9 = __VLS_pickFunctionalComponentCtx(AuthorIdentity, __VLS_6);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__more") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.toggleMore) }, ref: ("moreButtonRef"), ...{ class: ("post-card__metric post-card__more-button") }, type: ("button"), "aria-label": ("More actions"), "aria-haspopup": ("menu"), "aria-expanded": ((__VLS_ctx.moreOpen)), });
    // @ts-ignore
    (__VLS_ctx.moreButtonRef);
    // @ts-ignore
    [AppIcon,];
    const __VLS_10 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("more"), size: ((20)), }));
    const __VLS_11 = __VLS_10({ name: ("more"), size: ((20)), }, ...__VLS_functionalComponentArgsRest(__VLS_10));
    ({}({ name: ("more"), size: ((20)), }));
    // @ts-ignore
    [toggleMore, moreOpen, moreButtonRef,];
    const __VLS_14 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_11);
    if (__VLS_ctx.moreOpen) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ onKeydown: (__VLS_ctx.handleMenuKeydown) }, ref: ("menuRef"), ...{ class: ("post-card__menu") }, role: ("menu"), });
        // @ts-ignore
        (__VLS_ctx.menuRef);
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.copyLink) }, ref: ((__VLS_ctx.setMenuItemRef)), ...{ class: ("post-card__menu-item") }, type: ("button"), role: ("menuitem"), });
        // @ts-ignore
        [AppIcon,];
        const __VLS_15 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("link"), size: ((18)), }));
        const __VLS_16 = __VLS_15({ name: ("link"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_15));
        ({}({ name: ("link"), size: ((18)), }));
        // @ts-ignore
        [moreOpen, handleMenuKeydown, menuRef, copyLink, setMenuItemRef,];
        const __VLS_19 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_16);
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.copyActionLabel);
        // @ts-ignore
        [copyActionLabel,];
        if (__VLS_ctx.showNotInterested) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.handleNotInterested) }, ref: ((__VLS_ctx.setMenuItemRef)), ...{ class: ("post-card__menu-item") }, type: ("button"), role: ("menuitem"), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_20 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("eye-off"), size: ((18)), }));
            const __VLS_21 = __VLS_20({ name: ("eye-off"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_20));
            ({}({ name: ("eye-off"), size: ((18)), }));
            // @ts-ignore
            [setMenuItemRef, showNotInterested, handleNotInterested,];
            const __VLS_24 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_21);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        }
        if (__VLS_ctx.showDelete) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.requestDeletePost) }, ref: ((__VLS_ctx.setMenuItemRef)), ...{ class: ("post-card__menu-item post-card__menu-item--danger") }, type: ("button"), role: ("menuitem"), disabled: ((__VLS_ctx.deletePending)), "aria-busy": ((__VLS_ctx.deletePending)), });
            // @ts-ignore
            [AppIcon,];
            const __VLS_25 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("trash"), size: ((18)), }));
            const __VLS_26 = __VLS_25({ name: ("trash"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_25));
            ({}({ name: ("trash"), size: ((18)), }));
            // @ts-ignore
            [setMenuItemRef, showDelete, requestDeletePost, deletePending, deletePending,];
            const __VLS_29 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_26);
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        }
    }
    if (__VLS_ctx.copyState !== 'idle') {
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-card__copy-status") }, "aria-live": ("polite"), });
        (__VLS_ctx.copyActionLabel);
        // @ts-ignore
        [copyActionLabel, copyState,];
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__content") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ref: ("bodyRef"), ...{ class: ("post-card__body") }, ...{ class: (({ 'post-card__body--expanded': __VLS_ctx.bodyExpanded })) }, });
    // @ts-ignore
    (__VLS_ctx.bodyRef);
    __VLS_styleScopedClasses = ({ 'post-card__body--expanded': bodyExpanded });
    // @ts-ignore
    [LinkifiedText,];
    const __VLS_30 = __VLS_asFunctionalComponent(LinkifiedText, new LinkifiedText({ ...{ 'onInternalActivate': {} }, text: ((__VLS_ctx.post.content)), to: (({ name: 'PostDetail', params: { id: String(__VLS_ctx.post.id) } })), }));
    const __VLS_31 = __VLS_30({ ...{ 'onInternalActivate': {} }, text: ((__VLS_ctx.post.content)), to: (({ name: 'PostDetail', params: { id: String(__VLS_ctx.post.id) } })), }, ...__VLS_functionalComponentArgsRest(__VLS_30));
    ({}({ ...{ 'onInternalActivate': {} }, text: ((__VLS_ctx.post.content)), to: (({ name: 'PostDetail', params: { id: String(__VLS_ctx.post.id) } })), }));
    let __VLS_35;
    const __VLS_36 = {
        onInternalActivate: (__VLS_ctx.prepareDetailNavigation)
    };
    // @ts-ignore
    [post, post, bodyExpanded, bodyRef, prepareDetailNavigation,];
    const __VLS_34 = __VLS_pickFunctionalComponentCtx(LinkifiedText, __VLS_31);
    let __VLS_32;
    let __VLS_33;
    if (__VLS_ctx.bodyOverflowing && !__VLS_ctx.bodyExpanded) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.expandBody) }, ...{ class: ("post-card__show-more") }, type: ("button"), });
        // @ts-ignore
        [bodyExpanded, bodyOverflowing, expandBody,];
    }
    if (__VLS_ctx.post.quotePost || __VLS_ctx.post.replyToPost) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__reference") }, "aria-label": ("Referenced post"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("post-card__reference-label") }, });
        (__VLS_ctx.post.quotePost ? 'Quoted post' : 'Replying to');
        // @ts-ignore
        [post, post, post,];
        if (__VLS_ctx.referencePost?.deleted) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("post-card__reference-deleted") }, });
            // @ts-ignore
            [referencePost,];
        }
        else {
            if (__VLS_ctx.referenceAuthor) {
                // @ts-ignore
                [AuthorIdentity,];
                const __VLS_37 = __VLS_asFunctionalComponent(AuthorIdentity, new AuthorIdentity({ author: ((__VLS_ctx.referenceAuthor)), variant: ("compact"), }));
                const __VLS_38 = __VLS_37({ author: ((__VLS_ctx.referenceAuthor)), variant: ("compact"), }, ...__VLS_functionalComponentArgsRest(__VLS_37));
                ({}({ author: ((__VLS_ctx.referenceAuthor)), variant: ("compact"), }));
                // @ts-ignore
                [referenceAuthor, referenceAuthor,];
                const __VLS_41 = __VLS_pickFunctionalComponentCtx(AuthorIdentity, __VLS_38);
            }
            __VLS_elementAsFunction(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({ ...{ class: ("post-card__reference-content") }, });
            // @ts-ignore
            [LinkifiedText,];
            const __VLS_42 = __VLS_asFunctionalComponent(LinkifiedText, new LinkifiedText({ text: ((__VLS_ctx.referenceContent)), to: ((__VLS_ctx.referenceDestination)), }));
            const __VLS_43 = __VLS_42({ text: ((__VLS_ctx.referenceContent)), to: ((__VLS_ctx.referenceDestination)), }, ...__VLS_functionalComponentArgsRest(__VLS_42));
            ({}({ text: ((__VLS_ctx.referenceContent)), to: ((__VLS_ctx.referenceDestination)), }));
            // @ts-ignore
            [referenceContent, referenceDestination,];
            const __VLS_46 = __VLS_pickFunctionalComponentCtx(LinkifiedText, __VLS_43);
            if (__VLS_ctx.referenceMedia.length > 0 && __VLS_ctx.referenceDestination) {
                const __VLS_47 = {}.RouterLink;
                ({}.RouterLink);
                ({}.RouterLink);
                __VLS_components.RouterLink;
                __VLS_components.RouterLink;
                // @ts-ignore
                [RouterLink, RouterLink,];
                const __VLS_48 = __VLS_asFunctionalComponent(__VLS_47, new __VLS_47({ ...{ class: ("post-card__reference-media-link") }, to: ((__VLS_ctx.referenceDestination)), }));
                const __VLS_49 = __VLS_48({ ...{ class: ("post-card__reference-media-link") }, to: ((__VLS_ctx.referenceDestination)), }, ...__VLS_functionalComponentArgsRest(__VLS_48));
                ({}({ ...{ class: ("post-card__reference-media-link") }, to: ((__VLS_ctx.referenceDestination)), }));
                // @ts-ignore
                [PostMediaGrid,];
                const __VLS_53 = __VLS_asFunctionalComponent(PostMediaGrid, new PostMediaGrid({ media: ((__VLS_ctx.referenceMedia)), }));
                const __VLS_54 = __VLS_53({ media: ((__VLS_ctx.referenceMedia)), }, ...__VLS_functionalComponentArgsRest(__VLS_53));
                ({}({ media: ((__VLS_ctx.referenceMedia)), }));
                // @ts-ignore
                [referenceDestination, referenceDestination, referenceMedia, referenceMedia,];
                const __VLS_57 = __VLS_pickFunctionalComponentCtx(PostMediaGrid, __VLS_54);
                (__VLS_52.slots).default;
                const __VLS_52 = __VLS_pickFunctionalComponentCtx(__VLS_47, __VLS_49);
            }
        }
    }
    if (__VLS_ctx.post.media.length > 0) {
        const __VLS_58 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_59 = __VLS_asFunctionalComponent(__VLS_58, new __VLS_58({ ...{ 'onClick': {} }, ...{ class: ("post-card__media-link") }, to: (({ name: 'PostDetail', params: { id: String(__VLS_ctx.post.id) } })), }));
        const __VLS_60 = __VLS_59({ ...{ 'onClick': {} }, ...{ class: ("post-card__media-link") }, to: (({ name: 'PostDetail', params: { id: String(__VLS_ctx.post.id) } })), }, ...__VLS_functionalComponentArgsRest(__VLS_59));
        ({}({ ...{ 'onClick': {} }, ...{ class: ("post-card__media-link") }, to: (({ name: 'PostDetail', params: { id: String(__VLS_ctx.post.id) } })), }));
        let __VLS_64;
        const __VLS_65 = {
            onClick: (__VLS_ctx.prepareDetailNavigation)
        };
        // @ts-ignore
        [PostMediaGrid,];
        const __VLS_66 = __VLS_asFunctionalComponent(PostMediaGrid, new PostMediaGrid({ media: ((__VLS_ctx.post.media)), }));
        const __VLS_67 = __VLS_66({ media: ((__VLS_ctx.post.media)), }, ...__VLS_functionalComponentArgsRest(__VLS_66));
        ({}({ media: ((__VLS_ctx.post.media)), }));
        // @ts-ignore
        [post, post, post, prepareDetailNavigation,];
        const __VLS_70 = __VLS_pickFunctionalComponentCtx(PostMediaGrid, __VLS_67);
        (__VLS_63.slots).default;
        const __VLS_63 = __VLS_pickFunctionalComponentCtx(__VLS_58, __VLS_60);
        let __VLS_61;
        let __VLS_62;
    }
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("post-card__engagement") }, "aria-label": ("Engagement"), });
    const __VLS_71 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.RouterLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_72 = __VLS_asFunctionalComponent(__VLS_71, new __VLS_71({ ...{ 'onClick': {} }, ...{ class: ("post-card__metric post-card__reply") }, to: (({
            name: 'PostDetail',
            params: { id: String(__VLS_ctx.post.id) },
            query: { reply: '1' },
        })), "aria-label": ((__VLS_ctx.replyLabel)), }));
    const __VLS_73 = __VLS_72({ ...{ 'onClick': {} }, ...{ class: ("post-card__metric post-card__reply") }, to: (({
            name: 'PostDetail',
            params: { id: String(__VLS_ctx.post.id) },
            query: { reply: '1' },
        })), "aria-label": ((__VLS_ctx.replyLabel)), }, ...__VLS_functionalComponentArgsRest(__VLS_72));
    ({}({ ...{ 'onClick': {} }, ...{ class: ("post-card__metric post-card__reply") }, to: (({
            name: 'PostDetail',
            params: { id: String(__VLS_ctx.post.id) },
            query: { reply: '1' },
        })), "aria-label": ((__VLS_ctx.replyLabel)), }));
    let __VLS_77;
    const __VLS_78 = {
        onClick: (__VLS_ctx.prepareDetailNavigation)
    };
    // @ts-ignore
    [AppIcon,];
    const __VLS_79 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("reply"), size: ((18)), }));
    const __VLS_80 = __VLS_79({ name: ("reply"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_79));
    ({}({ name: ("reply"), size: ((18)), }));
    // @ts-ignore
    [post, prepareDetailNavigation, replyLabel,];
    const __VLS_83 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_80);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (__VLS_ctx.post.replyCount);
    // @ts-ignore
    [post,];
    (__VLS_76.slots).default;
    const __VLS_76 = __VLS_pickFunctionalComponentCtx(__VLS_71, __VLS_73);
    let __VLS_74;
    let __VLS_75;
    // @ts-ignore
    [RepostAction,];
    const __VLS_84 = __VLS_asFunctionalComponent(RepostAction, new RepostAction({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.post.id)), reposted: ((__VLS_ctx.post.repostStatus === 'ready' && __VLS_ctx.post.reposted)), count: ((__VLS_ctx.post.repostCount)), disabled: ((__VLS_ctx.repostUnavailable)), loading: ((__VLS_ctx.repostLoading)), pending: ((__VLS_ctx.repostPending)), ariaLabel: ((__VLS_ctx.repostLabel)), variant: ("compact"), }));
    const __VLS_85 = __VLS_84({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.post.id)), reposted: ((__VLS_ctx.post.repostStatus === 'ready' && __VLS_ctx.post.reposted)), count: ((__VLS_ctx.post.repostCount)), disabled: ((__VLS_ctx.repostUnavailable)), loading: ((__VLS_ctx.repostLoading)), pending: ((__VLS_ctx.repostPending)), ariaLabel: ((__VLS_ctx.repostLabel)), variant: ("compact"), }, ...__VLS_functionalComponentArgsRest(__VLS_84));
    ({}({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.post.id)), reposted: ((__VLS_ctx.post.repostStatus === 'ready' && __VLS_ctx.post.reposted)), count: ((__VLS_ctx.post.repostCount)), disabled: ((__VLS_ctx.repostUnavailable)), loading: ((__VLS_ctx.repostLoading)), pending: ((__VLS_ctx.repostPending)), ariaLabel: ((__VLS_ctx.repostLabel)), variant: ("compact"), }));
    let __VLS_89;
    const __VLS_90 = {
        onToggle: (__VLS_ctx.handleRepostActivation)
    };
    // @ts-ignore
    [post, post, post, post, repostUnavailable, repostLoading, repostPending, repostLabel, handleRepostActivation,];
    const __VLS_88 = __VLS_pickFunctionalComponentCtx(RepostAction, __VLS_85);
    let __VLS_86;
    let __VLS_87;
    // @ts-ignore
    [LikeAction,];
    const __VLS_91 = __VLS_asFunctionalComponent(LikeAction, new LikeAction({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.post.id)), liked: ((__VLS_ctx.post.likeStatus === 'ready' ? __VLS_ctx.post.liked : false)), count: ((__VLS_ctx.post.likeCount)), disabled: ((__VLS_ctx.likeUnavailable)), loading: ((__VLS_ctx.likeLoading)), pending: ((__VLS_ctx.likePending)), ariaLabel: ((__VLS_ctx.likeLabel)), "aria-pressed": ((__VLS_ctx.post.likeStatus === 'ready' ? __VLS_ctx.post.liked : null)), variant: ("compact"), }));
    const __VLS_92 = __VLS_91({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.post.id)), liked: ((__VLS_ctx.post.likeStatus === 'ready' ? __VLS_ctx.post.liked : false)), count: ((__VLS_ctx.post.likeCount)), disabled: ((__VLS_ctx.likeUnavailable)), loading: ((__VLS_ctx.likeLoading)), pending: ((__VLS_ctx.likePending)), ariaLabel: ((__VLS_ctx.likeLabel)), "aria-pressed": ((__VLS_ctx.post.likeStatus === 'ready' ? __VLS_ctx.post.liked : null)), variant: ("compact"), }, ...__VLS_functionalComponentArgsRest(__VLS_91));
    ({}({ ...{ 'onToggle': {} }, key: ((__VLS_ctx.post.id)), liked: ((__VLS_ctx.post.likeStatus === 'ready' ? __VLS_ctx.post.liked : false)), count: ((__VLS_ctx.post.likeCount)), disabled: ((__VLS_ctx.likeUnavailable)), loading: ((__VLS_ctx.likeLoading)), pending: ((__VLS_ctx.likePending)), ariaLabel: ((__VLS_ctx.likeLabel)), "aria-pressed": ((__VLS_ctx.post.likeStatus === 'ready' ? __VLS_ctx.post.liked : null)), variant: ("compact"), }));
    let __VLS_96;
    const __VLS_97 = {
        onToggle: (__VLS_ctx.handleLikeActivation)
    };
    // @ts-ignore
    [post, post, post, post, post, post, likeUnavailable, likeLoading, likePending, likeLabel, handleLikeActivation,];
    const __VLS_95 = __VLS_pickFunctionalComponentCtx(LikeAction, __VLS_92);
    let __VLS_93;
    let __VLS_94;
    const __VLS_98 = {}.RouterLink;
    ({}.RouterLink);
    ({}.RouterLink);
    __VLS_components.RouterLink;
    __VLS_components.RouterLink;
    // @ts-ignore
    [RouterLink, RouterLink,];
    const __VLS_99 = __VLS_asFunctionalComponent(__VLS_98, new __VLS_98({ ...{ 'onClick': {} }, ...{ class: ("post-card__metric post-card__views") }, to: (({
            name: 'PostDetail',
            params: { id: String(__VLS_ctx.post.id) },
        })), "aria-label": ((__VLS_ctx.viewActionLabel)), title: ((__VLS_ctx.viewActionLabel)), }));
    const __VLS_100 = __VLS_99({ ...{ 'onClick': {} }, ...{ class: ("post-card__metric post-card__views") }, to: (({
            name: 'PostDetail',
            params: { id: String(__VLS_ctx.post.id) },
        })), "aria-label": ((__VLS_ctx.viewActionLabel)), title: ((__VLS_ctx.viewActionLabel)), }, ...__VLS_functionalComponentArgsRest(__VLS_99));
    ({}({ ...{ 'onClick': {} }, ...{ class: ("post-card__metric post-card__views") }, to: (({
            name: 'PostDetail',
            params: { id: String(__VLS_ctx.post.id) },
        })), "aria-label": ((__VLS_ctx.viewActionLabel)), title: ((__VLS_ctx.viewActionLabel)), }));
    let __VLS_104;
    const __VLS_105 = {
        onClick: (__VLS_ctx.prepareDetailNavigation)
    };
    // @ts-ignore
    [AppIcon,];
    const __VLS_106 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("analytics"), size: ((18)), }));
    const __VLS_107 = __VLS_106({ name: ("analytics"), size: ((18)), }, ...__VLS_functionalComponentArgsRest(__VLS_106));
    ({}({ name: ("analytics"), size: ((18)), }));
    // @ts-ignore
    [post, prepareDetailNavigation, viewActionLabel, viewActionLabel,];
    const __VLS_110 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_107);
    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (__VLS_ctx.compactViewCount);
    // @ts-ignore
    [compactViewCount,];
    (__VLS_103.slots).default;
    const __VLS_103 = __VLS_pickFunctionalComponentCtx(__VLS_98, __VLS_100);
    let __VLS_101;
    let __VLS_102;
    if (__VLS_ctx.deleteConfirmOpen) {
        // @ts-ignore
        [ConfirmDialog,];
        const __VLS_111 = __VLS_asFunctionalComponent(ConfirmDialog, new ConfirmDialog({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete post?"), description: ("This post will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletePending)), error: ((__VLS_ctx.deleteError)), }));
        const __VLS_112 = __VLS_111({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete post?"), description: ("This post will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletePending)), error: ((__VLS_ctx.deleteError)), }, ...__VLS_functionalComponentArgsRest(__VLS_111));
        ({}({ ...{ 'onConfirm': {} }, ...{ 'onCancel': {} }, title: ("Delete post?"), description: ("This post will be permanently deleted. This can’t be undone."), confirmLabel: ("Delete"), cancelLabel: ("Cancel"), danger: (true), busy: ((__VLS_ctx.deletePending)), error: ((__VLS_ctx.deleteError)), }));
        let __VLS_116;
        const __VLS_117 = {
            onConfirm: (__VLS_ctx.confirmDeletePost)
        };
        const __VLS_118 = {
            onCancel: (__VLS_ctx.cancelDeletePost)
        };
        // @ts-ignore
        [deletePending, deleteConfirmOpen, deleteError, confirmDeletePost, cancelDeletePost,];
        const __VLS_115 = __VLS_pickFunctionalComponentCtx(ConfirmDialog, __VLS_112);
        let __VLS_113;
        let __VLS_114;
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['post-card'];
        __VLS_styleScopedClasses['post-card__header'];
        __VLS_styleScopedClasses['post-card__author-stack'];
        __VLS_styleScopedClasses['post-card__repost-context'];
        __VLS_styleScopedClasses['post-card__more'];
        __VLS_styleScopedClasses['post-card__metric'];
        __VLS_styleScopedClasses['post-card__more-button'];
        __VLS_styleScopedClasses['post-card__menu'];
        __VLS_styleScopedClasses['post-card__menu-item'];
        __VLS_styleScopedClasses['post-card__menu-item'];
        __VLS_styleScopedClasses['post-card__menu-item'];
        __VLS_styleScopedClasses['post-card__menu-item--danger'];
        __VLS_styleScopedClasses['post-card__copy-status'];
        __VLS_styleScopedClasses['post-card__content'];
        __VLS_styleScopedClasses['post-card__body'];
        __VLS_styleScopedClasses['post-card__show-more'];
        __VLS_styleScopedClasses['post-card__reference'];
        __VLS_styleScopedClasses['post-card__reference-label'];
        __VLS_styleScopedClasses['post-card__reference-deleted'];
        __VLS_styleScopedClasses['post-card__reference-content'];
        __VLS_styleScopedClasses['post-card__reference-media-link'];
        __VLS_styleScopedClasses['post-card__media-link'];
        __VLS_styleScopedClasses['post-card__engagement'];
        __VLS_styleScopedClasses['post-card__metric'];
        __VLS_styleScopedClasses['post-card__reply'];
        __VLS_styleScopedClasses['post-card__metric'];
        __VLS_styleScopedClasses['post-card__views'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                AuthorIdentity: AuthorIdentity,
                LinkifiedText: LinkifiedText,
                PostMediaGrid: PostMediaGrid,
                ConfirmDialog: ConfirmDialog,
                LikeAction: LikeAction,
                RepostAction: RepostAction,
                AppIcon: AppIcon,
                postCardRef: postCardRef,
                bodyRef: bodyRef,
                moreButtonRef: moreButtonRef,
                menuRef: menuRef,
                moreOpen: moreOpen,
                deleteConfirmOpen: deleteConfirmOpen,
                bodyExpanded: bodyExpanded,
                bodyOverflowing: bodyOverflowing,
                copyState: copyState,
                likeLoading: likeLoading,
                likeUnavailable: likeUnavailable,
                repostLoading: repostLoading,
                repostUnavailable: repostUnavailable,
                referencePost: referencePost,
                referenceDestination: referenceDestination,
                referenceAuthor: referenceAuthor,
                referenceContent: referenceContent,
                referenceMedia: referenceMedia,
                handleLikeActivation: handleLikeActivation,
                handleRepostActivation: handleRepostActivation,
                repostContextLabel: repostContextLabel,
                prepareDetailNavigation: prepareDetailNavigation,
                likeLabel: likeLabel,
                replyLabel: replyLabel,
                compactViewCount: compactViewCount,
                viewActionLabel: viewActionLabel,
                copyActionLabel: copyActionLabel,
                setMenuItemRef: setMenuItemRef,
                toggleMore: toggleMore,
                handleMenuKeydown: handleMenuKeydown,
                copyLink: copyLink,
                handleNotInterested: handleNotInterested,
                requestDeletePost: requestDeletePost,
                confirmDeletePost: confirmDeletePost,
                cancelDeletePost: cancelDeletePost,
                expandBody: expandBody,
                repostLabel: repostLabel,
            };
        },
        props: {},
        emits: {},
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    props: {},
    emits: {},
});
;
