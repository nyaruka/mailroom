# Smart Group Recalculation Implementation

## Problem
Smart groups were not being re-evaluated when contact fields were updated. This meant that if a contact's field value changed such that they should be added to or removed from a smart group, the change would not take effect until the next manual smart group recalculation.

## Solution
Added a new hook `RecalculateSmartGroups` that automatically recalculates smart group memberships whenever contact fields are updated.

## Implementation Details

### Files Modified/Created

1. **`core/runner/hooks/recalculate_smart_groups.go`** (NEW)
   - Contains the `RecalculateSmartGroups` hook
   - Executes with order 2 (after field updates but before modified_on updates)
   - Uses existing `models.CalculateDynamicGroups` function
   - Handles all contacts that had field changes in the current transaction

2. **`core/runner/handlers/contact_field_changed.go`** (MODIFIED)
   - Added `RecalculateSmartGroups` hook to the handler
   - Maintains proper execution order of hooks

3. **`core/runner/handlers/contact_field_changed_test.go`** (MODIFIED)
   - Added test case for smart group recalculation

4. **`core/runner/hooks/recalculate_smart_groups_test.go`** (NEW)
   - Unit test for the hook functionality

### Hook Execution Order

When a contact field changes, the following hooks are executed in order:

1. **`UpdateContactFields` (order 1)** - Updates field values in database
2. **`RecalculateSmartGroups` (order 2)** - Recalculates smart group memberships
3. **`UpdateCampaignFires` (order varies)** - Updates campaign triggers
4. **`UpdateContactModifiedOn` (order 100)** - Updates contact modified timestamp

### How It Works

1. Contact field change event is triggered
2. `handleContactFieldChanged` is called
3. Multiple hooks are attached including the new `RecalculateSmartGroups` hook
4. During commit, hooks are executed in order:
   - Field values are updated
   - Smart groups are recalculated for all affected contacts
   - Campaign fires are updated if needed
   - Contact modified timestamps are updated

### Benefits

- **Automatic**: No manual intervention required
- **Efficient**: Only recalculates for contacts with field changes
- **Consistent**: Uses existing smart group evaluation logic
- **Safe**: Executes within the same transaction as field updates

### Minimal Impact

- Uses existing `CalculateDynamicGroups` function (no new group evaluation logic)
- Follows established hook pattern (no new architectural concepts)
- Maintains existing execution order and dependencies
- No changes to database schema or API contracts