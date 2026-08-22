// Package route holds the account limiter, health, session affinity,
// account selection and lease state this proxy coordinates across its
// three upstream accounts. It performs no I/O of any kind: dispatch,
// persistence and logging belong to the packages that call into it.
//
// The concurrency invariants below are the properties the coordinator and
// its callers must hold jointly. Each is documented near the code that
// constrains it, and each has at least one test that can fail. Invariants
// owned by a later phase are listed with their owner so none is left
// without one.
//
//  1. Account and session state has one owner, the coordinator.
//     TestNewCoordinatorCreatesExactlyThreeAccounts.
//  2. Every shared-state read or write occurs under the coordinator mutex.
//     TestCoordinatorConcurrentAccessIsRaceFree (race detector).
//  3. No I/O occurs while holding the mutex. TestPackagePerformsNoIO.
//  4. Every acquired lease is released exactly once. TestReleaseIsReleaseOnce,
//     TestFinalizeIsReleaseOnce.
//  5. In-flight never becomes negative or exceeds twelve.
//     TestInFlightNeverExceedsTwelveUnderConcurrency,
//     TestReleaseNeverUnderflowsInFlight, TestReserveSkipsInFlightSaturatedAccount.
//  6. No account has more than 60 admitted starts in a rolling 60-second
//     interval. TestFirst60StartsAreAdmittedThe61stIsRejected,
//     TestExactBoundaryExpirationAdmitsCorrectly.
//  7. Retries acquire fresh leases. TestRetriesConsumeAnotherTimestampSkipsDoNot.
//  8. Waiting and backoff hold no leases. TestWaitReturnsNotifiedWhenTokenFires,
//     TestWaitTimesOutAtTheAccountAcquisitionCeiling.
//  9. Selection skips consume no rate capacity.
//     TestRetriesConsumeAnotherTimestampSkipsDoNot.
//  10. New-session pin creation and first account admission are atomic.
//     TestSelectForNewSessionConcurrentRequestsSharePin,
//     TestSelectForNewSessionInstallsProvisionalPin.
//  11. A stale request cannot overwrite a newer session-pin update.
//     TestConfirmPinRefusesStaleSequence.
//  12. Database insertion order does not define request sequence; sequence_no
//     does. Store schema_attempt_log tests.
//  13. Shutdown cannot close SQLite while active handlers may still append.
//     Owned by the shutdown phase.
//  14. Downstream commitment is a monotonic state transition. Owned by relay.
//  15. A committed response can never return to the retry state. Owned by relay.
//  16. Account health mutation and limiter admission use the same account
//     identity. TestSingle429GatesOnlyThatAccountForTheStatedDelay.
//  17. Pinned variants never create new account state.
//     TestBaseAndPinnedAliasesShareTheSameAccountCount.
//  18. Process logs and SQLite writes occur after coordinator unlock.
//     TestPackagePerformsNoIO.
//  19. Observer errors cannot affect response relay. Owned by relay.
//  20. Client cancellation propagates through waiting, backoff, upstream I/O
//     and database calls. TestWaitReturnsCanceledPromptly,
//     TestManyCanceledWaitersExitWithoutLeaking.
//  21. Every selection phase is bounded by 60 seconds and records one terminal
//     dispatch or selection failure.
//     TestWaitTimesOutAtTheAccountAcquisitionCeiling,
//     TestClassifySelectionFailureCapacityTimeout.
//  22. The first same-session request cannot split its provisional pin across
//     accounts under concurrent arrival.
//     TestSelectForNewSessionConcurrentRequestsSharePin.
//  23. No response header reaches the downstream writer until the
//     final-response state machine commits. Owned by relay.
//  24. A post-commit upstream read failure cannot return normally through the
//     HTTP handler. Owned by relay.
//  25. Pending skip facts are bounded by the fixed account/reason vocabulary.
//     TestSkipReasonFor, selection_failure tests.
//  26. No admission path grants an account an exception to disabled health
//     state. TestReserveSkipsDisabledAccount,
//     TestSelectExplicitDisabledReturnsImmediately.
//  27. No http.Client.Do occurs without a committed admission row, and no
//     admission row is committed without a held reservation.
//     TestNoDispatchWhenAdmissionCommitFails,
//     TestDispatchOccursWhenAdmissionCommitSucceeds.
//  28. Client cancellation and logical-deadline expiry cannot cancel admission
//     or terminal persistence before its own bounded store timeout.
//     Store admission cancellation tests.
//  29. Aggregate request-owned memory never exceeds the configured budget.
//     Owned by the resource gate.
//  30. An unconfirmed provisional pin with no remaining holders cannot stay
//     live. TestReleaseProvisionalHolderRemovesPinOnLastHolder.
//  31. The rolling window is measured over http.Client.Do invocation instants,
//     and a pending reservation occupies a slot from grant until finalize or
//     cancel. TestDispatchTimestampAnchoredAtFinalizeNotReservation.
//  32. No dispatch occurs during the first full rolling window of a process's
//     life. TestNoDispatchAdmittedDuringTheBlackout,
//     TestFirstAdmissionAfterTheBlackoutSucceeds.
//  33. Live accepted client connections never exceed the configured ceiling.
//     Owned by the resource gate.
package route
