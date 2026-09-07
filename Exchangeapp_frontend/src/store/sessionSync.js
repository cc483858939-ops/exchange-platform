let homeTimelineSync = null;
let profileSessionSync = null;
let searchSessionSync = null;
let historySessionSync = null;
let connectionsSessionSync = null;
export const registerHomeTimelineSync = (sync) => {
    homeTimelineSync = sync;
};
export const registerProfileSessionSync = (sync) => {
    profileSessionSync = sync;
};
export const registerSearchSessionSync = (sync) => {
    searchSessionSync = sync;
};
export const registerHistorySessionSync = (sync) => {
    historySessionSync = sync;
};
export const registerConnectionsSessionSync = (sync) => {
    connectionsSessionSync = sync;
};
export const syncHomeLikeState = (update) => {
    const profileApplied = profileSessionSync?.applyLikeStateUpdateLocal(update) ?? false;
    const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
    return profileApplied || historyApplied;
};
export const syncHomeRepostState = (update) => {
    const profileApplied = profileSessionSync?.applyRepostStateUpdateLocal(update) ?? false;
    const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
    return profileApplied || historyApplied;
};
export const syncHomePostRemoval = (postID) => {
    profileSessionSync?.removePostEverywhereLocal(postID);
    historySessionSync?.removePostLocal(postID);
};
export const syncHomeAuthorIdentity = (author) => {
    profileSessionSync?.replaceAuthorIdentityEverywhereLocal(author);
    historySessionSync?.replaceAuthorIdentityLocal(author);
    connectionsSessionSync?.replaceUserIdentityLocal(author);
};
export const syncProfileLikeState = (update) => {
    const homeApplied = homeTimelineSync?.applyLikeStateUpdateLocal(update) ?? false;
    const historyApplied = historySessionSync?.applyExternalLikeStateLocal(update) ?? false;
    return homeApplied || historyApplied;
};
export const syncProfileRepostState = (update) => {
    const homeApplied = homeTimelineSync?.applyRepostStateUpdateLocal(update) ?? false;
    const historyApplied = historySessionSync?.applyExternalRepostStateLocal(update) ?? false;
    return homeApplied || historyApplied;
};
export const syncProfilePostRemoval = (postID) => {
    homeTimelineSync?.removePostLocal(postID);
    historySessionSync?.removePostLocal(postID);
};
export const syncProfileAuthorIdentity = (author) => {
    homeTimelineSync?.replaceAuthorIdentityLocal(author);
    historySessionSync?.replaceAuthorIdentityLocal(author);
    connectionsSessionSync?.replaceUserIdentityLocal(author);
};
export const syncExternalPostLikeState = (update) => {
    homeTimelineSync?.applyExternalLikeStateLocal(update);
    profileSessionSync?.applyExternalLikeStateLocal(update);
    historySessionSync?.applyExternalLikeStateLocal(update);
};
export const syncExternalPostRepostState = (update) => {
    homeTimelineSync?.applyExternalRepostStateLocal(update);
    profileSessionSync?.applyExternalRepostStateLocal(update);
    historySessionSync?.applyExternalRepostStateLocal(update);
};
export const syncExternalPostRemoval = (postID) => {
    homeTimelineSync?.removePostLocal(postID);
    profileSessionSync?.removePostEverywhereLocal(postID);
    historySessionSync?.removePostLocal(postID);
};
export const syncExternalReplyCount = (update) => {
    homeTimelineSync?.applyReplyCountUpdateLocal(update);
    profileSessionSync?.applyReplyCountUpdateEverywhereLocal(update);
    historySessionSync?.applyReplyCountUpdateLocal(update);
};
export const syncProfileFollowState = (state) => {
    homeTimelineSync?.reconcileFollowStateLocal(state);
    searchSessionSync?.applyExternalFollowStateLocal(state);
    connectionsSessionSync?.applyExternalFollowStateLocal(state);
};
export const syncExternalFollowState = (state) => {
    homeTimelineSync?.reconcileFollowStateLocal(state);
    profileSessionSync?.applyExternalFollowStateLocal(state);
    searchSessionSync?.applyExternalFollowStateLocal(state);
    connectionsSessionSync?.applyExternalFollowStateLocal(state);
};
